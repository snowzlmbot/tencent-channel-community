"""Synthetic metadata-only unittest suite, intended for the GitHub Actions gate.

No installed executable, network, credentials, SDK or third-party libraries.
"""

from contextlib import redirect_stdout
import importlib.util
import io
import json
from pathlib import Path
import struct
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location(
    'recover_go_metadata', Path(__file__).resolve().parents[1] / 'scripts' / 'recover_go_metadata.py')
metadata = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(metadata)

TEXT_ADDR = 0x100001000
HEADER_OFFSET = 640


def fixture(section_name='__gopclntab', segment='__DATA_CONST', prefix=b'', text_start=TEXT_ADDR):
    """Two funcs, one selected, with absolute paths solely as synthetic input."""
    names = b'qqcli/commands/pd/manage.deliverToTarget\0runtime.unrelated\0'
    files = b'/private/synthetic-developer/project/manage.go\0'
    offsets = [72, 72 + len(names), 76 + len(names), 76 + len(names) + len(files)]
    offsets.append(offsets[3] + 4)
    pcln = bytearray(metadata.MAGIC + struct.pack('<8Q', 2, 1, text_start, *offsets))
    pcln += names + struct.pack('<I', 0) + files + b'\0\x02\x08\0'
    # functab is 2 pairs and final entry PC, followed by two _func records.
    pcln += struct.pack('<5I', 0, 20, 16, 64, 32)
    for entry, nameoff in ((0, 0), (16, names.index(b'runtime'))):
        record = bytearray(44)
        struct.pack_into('<Ii', record, 0, entry, nameoff)
        struct.pack_into('<I', record, 20, 1)  # pcfile stream: initial delta +1 => file 0
        struct.pack_into('<I', record, 32, 0)  # CU file index start
        pcln += record
    data = bytearray(HEADER_OFFSET + len(prefix) + len(pcln))
    struct.pack_into('<8I', data, 0, 0xFEEDFACF, 0x0100000C, 0, 2, 2, 304, 0, 0)

    def segment_command(at, segname, secname, addr, offset, length):
        seg = segname.encode().ljust(16, b'\0')
        struct.pack_into('<II16s4Q4I', data, at, 0x19, 152, seg,
                         addr, length, offset, length, 7, 5, 1, 0)
        struct.pack_into('<16s16sQQ8I', data, at + 72,
                         secname.encode().ljust(16, b'\0'), seg, addr, length,
                         offset, 2, 0, 0, 0, 0, 0, 0)

    segment_command(32, '__TEXT', '__text', TEXT_ADDR, 512, 64)
    segment_command(184, segment, section_name, TEXT_ADDR + 0x1000,
                    HEADER_OFFSET, len(prefix) + len(pcln))
    data[HEADER_OFFSET:] = prefix + pcln
    return data


class Recovery(unittest.TestCase):
    def test_named_section_in_data_const_and_text(self):
        for segment in ('__DATA_CONST', '__TEXT'):
            with self.subTest(segment=segment):
                result = metadata.recover(fixture(segment=segment))
                self.assertEqual(result['total_function_count'], 2)
                self.assertEqual(result['selected_function_count'], 1)
                self.assertEqual(result['functions'], [{
                    'name': 'qqcli/commands/pd/manage.deliverToTarget',
                    'entry_address': hex(TEXT_ADDR), 'size_bytes': 16,
                    'source_file': 'manage.go'}])
                self.assertEqual(result['text_base_source'], 'pcHeader')
                self.assertNotIn('synthetic-developer', json.dumps(result))
                self.assertNotIn('/private', json.dumps(result))

    def test_rodata_scan_skips_false_header(self):
        # Earlier magic matches but an invalid header must not mask the valid candidate.
        prefix = metadata.MAGIC + bytes(72)
        result = metadata.recover(fixture(section_name='__rodata', prefix=prefix))
        self.assertEqual(result['selected_function_count'], 1)

    def test_zero_text_start_uses_section_base(self):
        result = metadata.recover(fixture(text_start=0))
        self.assertEqual(result['text_base_source'], '__text')
        self.assertEqual(result['functions'][0]['entry_address'], hex(TEXT_ADDR))

    def test_reject_unrecognized_text_mapping(self):
        with self.assertRaises(metadata.MetadataError):
            metadata.recover(fixture(text_start=TEXT_ADDR + 16))

    def test_fixed_selection_excludes_auth_poll_and_runtime(self):
        for name in ('qqcli/internal/subscription.IsNewNotice',
                     'qqcli/commands/pd/manage.pollDMNotices',
                     'qqcli/commands/pd/manage.safeDeliver', 'qqcli/cmd.showSubscribeHint'):
            self.assertTrue(metadata.selected(name), name)
        for name in ('runtime.netpoll', 'qqcli/internal/auth.PollDeviceToken',
                     '/private/foo/internal/subscription.Secret',
                     'type:.eq./private/foo/internal/subscription.Secret',
                     'qqcli/internal/subscription.bad\nsecret',
                     'qqcli/internal/subscription../bad'):
            self.assertFalse(metadata.selected(name), name)

    def test_optional_bad_source_mapping_is_omitted(self):
        data = fixture()
        offsets = struct.unpack_from('<5Q', data, HEADER_OFFSET + 32)
        table = HEADER_OFFSET + offsets[4]
        struct.pack_into('<I', data, table + 20 + 20, 0xFFFFFFFF)
        result = metadata.recover(data)
        self.assertNotIn('source_file', result['functions'][0])

    def test_reject_bad_macho_commands_and_sections(self):
        changes = ((0, '<I', 0), (4, '<I', 7), (20, '<I', 0xFFFFFFFF),
                   (36, '<I', 0), (36, '<I', 153), (96, '<I', 1000),
                   (32 + 72 + 48, '<I', 0xFFFFFFFF),
                   (32 + 72 + 40, '<Q', 0xFFFFFFFFFFFFFFFF))
        for offset, fmt, value in changes:
            data = fixture()
            struct.pack_into(fmt, data, offset, value)
            with self.subTest(offset=offset, value=value), self.assertRaises(metadata.MetadataError):
                metadata.recover(data)

    def test_truncated_macho(self):
        data = fixture()
        for length in (0, 4, 31, 32, 100, 335, len(data) - 1):
            with self.subTest(length=length), self.assertRaises(metadata.MetadataError):
                metadata.recover(data[:length])

    def test_reject_invalid_header_counts_and_offsets(self):
        for offset, value in ((8, 0), (8, metadata.MAX_FUNCTIONS + 1),
                              (32, 71), (40, 0), (48, 0xFFFFFFFF),
                              (64, 0xFFFFFFFF)):
            data = fixture()
            struct.pack_into('<Q', data, HEADER_OFFSET + offset, value)
            with self.subTest(offset=offset), self.assertRaises(metadata.MetadataError):
                metadata.recover(data)

    def test_reject_bad_functab_records_and_names(self):
        original = fixture()
        table = HEADER_OFFSET + struct.unpack_from('<Q', original, HEADER_OFFSET + 64)[0]
        for offset, fmt, value in ((table + 4, '<I', 0),
                                   (table + 4, '<I', 0xFFFFFFFF),
                                   (table + 8, '<I', 0),
                                   (table + 16, '<I', 65),
                                   (table + 20, '<I', 1),
                                   (table + 24, '<i', -1),
                                   (table + 24, '<i', 0x7FFFFFFF)):
            data = bytearray(original)
            struct.pack_into(fmt, data, offset, value)
            with self.subTest(offset=offset, value=value), self.assertRaises(metadata.MetadataError):
                metadata.recover(data)

    def test_strings_fail_closed(self):
        for raw in (b'no terminator', b'\xff\0'):
            with self.assertRaises(metadata.MetadataError):
                metadata.cstring(raw, 0, len(raw))


class CommandLine(unittest.TestCase):
    def test_stdout_json_and_exclusive_output_creation(self):
        with tempfile.TemporaryDirectory() as directory:
            binary = Path(directory) / 'synthetic-macho'
            output = Path(directory) / 'metadata.json'
            binary.write_bytes(fixture())
            stdout = io.StringIO()
            with redirect_stdout(stdout):
                self.assertEqual(metadata.main(['--binary', str(binary)]), 0)
            self.assertEqual(json.loads(stdout.getvalue())['selected_function_count'], 1)
            self.assertEqual(metadata.main(['--binary', str(binary), '--output', str(output)]), 0)
            previous = output.read_bytes()
            with redirect_stdout(io.StringIO()):
                self.assertEqual(metadata.main(['--binary', str(binary), '--output', str(output)]), 1)
                self.assertEqual(metadata.main(['--binary', str(binary), '--output', str(binary)]), 1)
            self.assertEqual(previous, output.read_bytes())
            self.assertEqual(binary.read_bytes(), fixture())

    def test_failure_is_json_without_private_paths_or_exception_content(self):
        stdout = io.StringIO()
        with redirect_stdout(stdout):
            status = metadata.main(['--binary', '/private/synthetic-secret/no-such-binary'])
        self.assertEqual(status, 1)
        self.assertIn('error', json.loads(stdout.getvalue()))
        self.assertNotIn('/private', stdout.getvalue())
        self.assertNotIn('synthetic-secret', stdout.getvalue())


if __name__ == '__main__':
    unittest.main()
