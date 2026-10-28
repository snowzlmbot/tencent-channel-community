#!/usr/bin/env python3
"""Read-only, dependency-free Mach-O/Go metadata recovery (not source recovery).

Supports thin little-endian arm64 Mach-O and the Go 1.20+ 0xfffffff1
pclntab layout. No code is executed or disassembled. Only fixed notification
symbol families and sanitized source basenames are exported; embedded full
paths, code bytes, arbitrary strings and input filenames are never exported.
Function sizes are linker table entry spans, not source-level body sizes.
"""

import argparse
import json
from pathlib import Path
import re
import struct
import sys

DEFAULT_BINARY = Path('/opt/homebrew/lib/node_modules/tencent-channel-cli/node_modules/'
                      'tencent-channel-cli-darwin-arm64/bin/tencent-channel-cli')
MAGIC = b'\xf1\xff\xff\xff\x00\x00\x04\x08'
MAX_FUNCTIONS = 2_000_000
MAX_STRING = 4096


class MetadataError(ValueError):
    """Unsupported or malformed metadata; messages never include input data."""


def require(condition, message='Malformed metadata bounds'):
    if not condition:
        raise MetadataError(message)


def unpack(data, fmt, offset, end=None):
    size = struct.calcsize(fmt)
    require(0 <= offset <= (len(data) if end is None else end) - size)
    return struct.unpack_from(fmt, data, offset)


def cstring(data, offset, end):
    require(0 <= offset < end <= len(data))
    stop = data.find(b'\0', offset, min(end, offset + MAX_STRING))
    require(stop >= 0, 'Unterminated metadata string')
    try:
        return data[offset:stop].decode('utf-8', errors='strict')
    except UnicodeDecodeError:
        raise MetadataError('Invalid metadata string encoding') from None


def macho_sections(data):
    """Validate LC_SEGMENT_64 commands and return file-backed sections."""
    header = unpack(data, '<8I', 0)
    require(header[0] == 0xFEEDFACF and header[1] == 0x0100000C,
            'Expected thin little-endian arm64 Mach-O')
    ncmds, sizeofcmds = header[4:6]
    end = 32 + sizeofcmds
    require(end <= len(data) and ncmds <= sizeofcmds // 8)
    offset = 32
    sections = []
    for _ in range(ncmds):
        cmd, size = unpack(data, '<II', offset, end)
        require(size >= 8 and size % 8 == 0 and offset + size <= end)
        if cmd == 0x19:  # LC_SEGMENT_64
            require(size >= 72)
            vmaddr, vmsize, fileoff, filesize = unpack(data, '<4Q', offset + 24, offset + size)
            nsects = unpack(data, '<I', offset + 64, offset + size)[0]
            require(nsects <= (size - 72) // 80 and fileoff + filesize <= len(data))
            for index in range(nsects):
                start = offset + 72 + index * 80
                raw_name, raw_segment = unpack(data, '<16s16s', start, offset + size)
                try:
                    name = raw_name.split(b'\0', 1)[0].decode('ascii')
                    segment = raw_segment.split(b'\0', 1)[0].decode('ascii')
                except UnicodeDecodeError:
                    raise MetadataError('Invalid section name') from None
                addr, length, position = unpack(data, '<QQI', start + 32, offset + size)
                flags = unpack(data, '<I', start + 64, offset + size)[0]
                require(vmaddr <= addr and addr + length <= vmaddr + vmsize)
                if flags & 0xFF in (1, 12, 18):  # zerofill, GB zerofill, TLS zerofill
                    continue
                require(fileoff <= position and position + length <= fileoff + filesize)
                sections.append({'name': name, 'segment': segment, 'addr': addr,
                                 'offset': position, 'size': length})
        offset += size
    require(offset == end)
    return sections


def selected(name):
    """Fixed scope: subscription package, notice poll/delivery, CLI hints."""
    if not re.fullmatch(r'[A-Za-z0-9_./()*\[\],:+~<>={}\-·]+', name):
        return False
    if name.startswith(('/', '\\')) or '..' in name or ':/' in name:
        return False
    if not name.startswith(('qqcli/', 'type:.eq.qqcli/', 'type:.hash.qqcli/')):
        return False
    lower = name.lower()
    return ('/internal/subscription.' in lower
            or ('/commands/pd/manage.' in lower
                and any(word in lower for word in ('poll', 'deliver', 'subscr')))
            or ('/cmd.' in lower and ('subscr' in lower)))


def source_basename(data, base, offsets, record, nfiles, end):
    """Resolve the entry PC's pcfile value; optional data failures return None.

    Go pc-value streams start at -1; the initial zigzag delta determines the
    file at the function entry. cutab translates compilation-unit file indexes
    to filetab string offsets. Full paths are never returned, even on errors.
    """
    try:
        pcfile = unpack(data, '<I', record + 20, end)[0]
        cu = unpack(data, '<I', record + 32, end)[0]
        if not pcfile or cu == 0xFFFFFFFF:
            return None
        pos = base + offsets[3] + pcfile
        pc_end = base + offsets[4]
        value, shift = 0, 0
        while True:
            require(pos < pc_end and shift < 35)
            byte = data[pos]
            pos += 1
            value |= (byte & 127) << shift
            if byte < 128:
                break
            shift += 7
        file_index = -1 + ((value >> 1) ^ -(value & 1))
        if file_index < 0:
            return None
        entry = base + offsets[1] + (cu + file_index) * 4
        fileoff = unpack(data, '<I', entry, base + offsets[2])[0]
        require(nfiles > 0 and fileoff < offsets[3] - offsets[2])
        path = cstring(data, base + offsets[2] + fileoff, base + offsets[3])
        basename = path.replace('\\', '/').rsplit('/', 1)[-1]
        return basename if re.fullmatch(r'[A-Za-z0-9_][A-Za-z0-9_.-]*\.go', basename) else None
    except (MetadataError, IndexError):
        return None


def parse_pclntab(data, section, position, text):
    end = section['offset'] + section['size']
    require(data[position:position + 8] == MAGIC, 'Unsupported Go pclntab layout')
    nfunc, nfiles, text_start, *offsets = unpack(data, '<8Q', position + 8, end)
    require(0 < nfunc <= MAX_FUNCTIONS)
    require(72 <= offsets[0] < offsets[1] <= offsets[2] <= offsets[3] <= offsets[4] < end - position)
    table = position + offsets[4]
    table_size = nfunc * 8 + 4  # final entry PC has no associated _func
    require(table + table_size <= end)
    # Externally linked Darwin Go binaries may leave pcHeader.textStart zero.
    # Runtime moduledata then supplies __text's address. Do not guess other bases.
    text_start = text_start or text['addr']
    require(text_start == text['addr'], 'Unsupported Go text address mapping')
    names_start, names_end = position + offsets[0], position + offsets[1]
    result = []
    previous = -1
    for index in range(nfunc):
        entry, funcoff = unpack(data, '<II', table + index * 8, end)
        next_entry = unpack(data, '<I', table + (index + 1) * 8, end)[0]
        require(previous < entry < next_entry <= text['size'])
        previous = entry
        record = table + funcoff
        require(record >= table + table_size and record + 44 <= end)
        record_entry, nameoff = unpack(data, '<Ii', record, end)
        require(record_entry == entry and 0 <= nameoff < names_end - names_start)
        name = cstring(data, names_start + nameoff, names_end)
        if not selected(name):
            continue
        row = {'name': name, 'entry_address': hex(text_start + entry),
               'size_bytes': next_entry - entry}
        filename = source_basename(data, position, offsets, record, nfiles, end)
        if filename is not None:
            row['source_file'] = filename
        result.append(row)
    return {'format': 'macho-arm64-go-pclntab', 'layout': 'go1.20+',
            'address_kind': 'unslid_virtual_address',
            'text_base_source': 'pcHeader' if unpack(data, '<Q', position + 24, end)[0] else '__text',
            'total_function_count': nfunc, 'selected_function_count': len(result),
            'functions': result,
            'limitations': ['Metadata only; not original source or decompilation.',
                            'Sizes are functab entry spans; filenames are basenames only.',
                            'Inlined functions and missing/unsupported source mappings may be absent.']}


def recover(data):
    sections = macho_sections(data)
    texts = [section for section in sections if section['name'] == '__text']
    require(len(texts) == 1, 'Expected one text section')
    # Prefer the named section, independently of its segment (__TEXT or __DATA_CONST).
    candidates = sorted((s for s in sections if s['name'] in ('__gopclntab', '__rodata')),
                        key=lambda s: s['name'] != '__gopclntab')
    for section in candidates:
        start, end = section['offset'], section['offset'] + section['size']
        pos = data.find(MAGIC, start, end)
        while pos >= 0:
            try:
                return parse_pclntab(data, section, pos, texts[0])
            except MetadataError:
                pos = data.find(MAGIC, pos + 1, end)
    raise MetadataError('No valid supported Go pclntab found')


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, default=DEFAULT_BINARY)
    parser.add_argument('--output', type=Path, help='Create a new JSON file; never overwrite existing files')
    args = parser.parse_args(argv)
    try:
        result = recover(args.binary.read_bytes())
        encoded = json.dumps(result, ensure_ascii=True, indent=2) + '\n'
        if args.output:
            # Exclusive creation prevents clobbering the input, symlinks or live files.
            with args.output.open('x', encoding='utf-8') as output:
                output.write(encoded)
        else:
            sys.stdout.write(encoded)
        return 0
    except (MetadataError, OSError):
        # Do not echo OS exceptions, paths, arbitrary metadata or original bytes.
        sys.stdout.write(json.dumps({'error': 'Unable to recover supported notification metadata or create output'}) + '\n')
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
