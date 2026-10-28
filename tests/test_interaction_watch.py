"""Offline unittest coverage, intended for the cloud Actions gate (no real CLI)."""

from contextlib import redirect_stderr, redirect_stdout
import importlib.util
import io
import json
import os
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location(
    "interaction_watch", Path(__file__).resolve().parents[1] / "scripts" / "interaction_watch.py")
watch = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(watch)


def payload(value="first-private-content"):
    return {"success": True, "data": {"unknown_schema": [{"private_field": value}], "has_more": False}}


class Transitions(unittest.TestCase):
    def test_baseline_silent_change_duplicate_and_ack(self):
        initial, notice = watch.process_payload(payload())
        self.assertIsNone(notice)
        same, notice = watch.process_payload(payload(), initial)
        self.assertEqual(same, initial)
        self.assertIsNone(notice)
        pending, notice = watch.process_payload(payload("second-private-content"), initial)
        self.assertEqual(pending["digest"], initial["digest"])
        self.assertEqual(notice, {"status": "changed", "message": watch.MESSAGE,
                                  "delivery_id": pending["pending_digest"]})
        # Pending retries take precedence even over a missing/failed next response.
        retried, repeated = watch.process_payload(None, pending)
        self.assertEqual((retried, repeated), (pending, notice))
        committed = watch.acknowledge(pending, notice["delivery_id"])
        self.assertEqual(committed["digest"], pending["pending_digest"])
        self.assertIsNone(committed["pending_digest"])
        self.assertIsNone(watch.process_payload(payload("second-private-content"), committed)[1])

    def test_metadata_only_observed_response_valid(self):
        baseline, notice = watch.process_payload({"success": True, "data": {"has_more": False}})
        self.assertIsNone(notice)
        state, notice = watch.process_payload(
            {"success": True, "data": {"has_more": True, "attach_info": "page-cursor"}}, baseline)
        self.assertEqual(state, baseline)
        self.assertIsNone(notice)

    def test_unknown_nested_metadata_is_not_ignored(self):
        state, _ = watch.process_payload({"success": True, "data": [{"has_more": False}]})
        self.assertIsNotNone(watch.process_payload(
            {"success": True, "data": [{"has_more": True}]}, state)[1])

    def test_canonical_object_order_but_list_order_preserved(self):
        state, _ = watch.process_payload({"success": True, "data": {"b": 2, "a": 1}})
        self.assertIsNone(watch.process_payload(
            {"success": True, "data": {"a": 1, "b": 2}}, state)[1])
        state, _ = watch.process_payload({"success": True, "data": [1, 2]})
        self.assertIsNotNone(watch.process_payload({"success": True, "data": [2, 1]}, state)[1])

    def test_malformed_or_failed_payload_does_not_mutate_state(self):
        state, _ = watch.process_payload(payload())
        original = dict(state)
        bad = [None, [], {}, {"success": 1, "data": [1]}, {"success": "true", "data": [1]},
               {"success": False, "data": [1]}, {"success": True},
               *({"success": True, "data": value} for value in (None, {}, [], "secret", 0)),
               {"success": True, "data": [float("nan")]}]
        for item in bad:
            with self.subTest(item=item), self.assertRaises(watch.WatchError):
                watch.process_payload(item, state)
            self.assertEqual(state, original)

    def test_invalid_state_and_ack(self):
        state, _ = watch.process_payload(payload())
        for invalid in ({}, {**state, "version": True}, {**state, "digest": "x"},
                        {**state, "digest": "A" * 64}, {**state, "unexpected": 1},
                        {**state, "pending_digest": state["digest"]}):
            with self.subTest(invalid=invalid), self.assertRaises(watch.WatchError):
                watch.process_payload(payload(), invalid)
        pending, _ = watch.process_payload(payload("changed"), state)
        original = dict(pending)
        for wrong in (state["digest"], "not-a-digest", "0" * 64):
            with self.assertRaises(watch.WatchError):
                watch.acknowledge(pending, wrong)
            self.assertEqual(pending, original)
        with self.assertRaises(watch.WatchError):
            watch.acknowledge(state, state["digest"])

    def test_json_bounds_and_strict_decoding(self):
        for raw in (b"", b"x", b"\xff", b'{"a":1,"a":2}', b'{"a":NaN}',
                    b" " * (watch.MAX_JSON_BYTES + 1)):
            with self.subTest(raw_length=len(raw)), self.assertRaises(watch.WatchError):
                watch.parse_json(raw)


class FilesAndCLI(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.path = Path(self.directory.name) / "private" / "nested" / "watch.json"

    def invoke(self, *extra):
        stdout, stderr = io.StringIO(), io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr):
            code = watch.main(["--state", str(self.path), *extra])
        return code, stdout.getvalue(), stderr.getvalue()

    def baseline(self):
        with mock.patch.object(watch, "query_payload", return_value=payload()):
            self.assertEqual(self.invoke(), (0, "", ""))

    def test_atomic_state_and_new_directory_permissions(self):
        self.baseline()
        self.assertEqual(stat.S_IMODE(self.path.stat().st_mode), 0o600)
        self.assertEqual(stat.S_IMODE(Path(str(self.path) + ".lock").stat().st_mode), 0o600)
        self.assertEqual(stat.S_IMODE(self.path.parent.stat().st_mode), 0o700)
        self.assertEqual(stat.S_IMODE(self.path.parent.parent.stat().st_mode), 0o700)
        raw = self.path.read_text()
        self.assertNotIn("private-content", raw)
        self.assertEqual(set(json.loads(raw)), {"version", "digest", "pending_digest"})
        self.assertFalse(list(self.path.parent.glob(".interaction-watch-*")))

    def test_existing_parent_permissions_unchanged(self):
        self.path.parent.mkdir(parents=True)
        self.path.parent.chmod(0o755)
        self.baseline()
        self.assertEqual(stat.S_IMODE(self.path.parent.stat().st_mode), 0o755)

    def test_pending_is_durable_and_retry_ack_never_query_cli(self):
        self.baseline()
        with mock.patch.object(watch, "query_payload", return_value=payload("changed-private-content")):
            code, output, errors = self.invoke()
        self.assertEqual((code, errors), (0, ""))
        self.assertNotIn("private", output)
        notice = json.loads(output)
        pending_bytes = self.path.read_bytes()
        with mock.patch.object(watch, "query_payload", side_effect=AssertionError("must not query")):
            self.assertEqual(self.invoke(), (0, output, ""))
            code, out, err = self.invoke("--ack", "0" * 64)
            self.assertEqual((code, out), (1, ""))
            self.assertEqual(json.loads(err)["status"], "error")
            self.assertEqual(self.path.read_bytes(), pending_bytes)
            self.assertEqual(self.invoke("--ack", notice["delivery_id"]), (0, "", ""))
        committed = json.loads(self.path.read_text())
        self.assertEqual(committed["digest"], notice["delivery_id"])
        self.assertIsNone(committed["pending_digest"])

    def test_failures_leave_state_byte_identical_and_no_raw_error(self):
        self.baseline()
        previous = self.path.read_bytes()
        for error in (watch.WatchError("secret-response"), OSError("secret-path"),
                      subprocess.TimeoutExpired("secret-command", 30, output=b"secret-body")):
            with mock.patch.object(watch, "query_payload", side_effect=error):
                code, out, err = self.invoke()
            self.assertEqual((code, out), (1, ""))
            self.assertEqual(json.loads(err)["status"], "error")
            self.assertNotIn("secret", err)
            self.assertEqual(self.path.read_bytes(), previous)

    def test_invalid_state_fails_before_query(self):
        self.baseline()
        self.path.write_text('{"version":1,"digest":"bad"}')
        before = self.path.read_bytes()
        with mock.patch.object(watch, "query_payload") as query:
            self.assertEqual(self.invoke()[0], 1)
            query.assert_not_called()
        self.assertEqual(self.path.read_bytes(), before)

    def test_state_lock_and_directory_symlinks_refused(self):
        self.path.parent.mkdir(parents=True)
        target = Path(self.directory.name) / "untouched"
        target.write_text("sensitive-do-not-read-or-replace")
        target.chmod(0o600)
        for path in (self.path, Path(str(self.path) + ".lock")):
            with self.subTest(path=path):
                path.symlink_to(target)
                with mock.patch.object(watch, "query_payload") as query:
                    self.assertEqual(self.invoke()[0], 1)
                    query.assert_not_called()
                self.assertEqual(target.read_text(), "sensitive-do-not-read-or-replace")
                path.unlink()
        linked = Path(self.directory.name) / "linked"
        linked.symlink_to(self.path.parent, target_is_directory=True)
        self.path = linked / "other.json"
        with mock.patch.object(watch, "query_payload") as query:
            self.assertEqual(self.invoke()[0], 1)
            query.assert_not_called()

    def test_lock_contention_is_bounded(self):
        with watch.locked_state(self.path):
            with mock.patch.object(watch, "query_payload") as query:
                self.assertEqual(self.invoke()[0], 1)
                query.assert_not_called()

    def test_subprocess_contract(self):
        result = subprocess.CompletedProcess(watch.COMMAND, 0, b'{"success":true,"data":{"has_more":false}}', b'')
        with mock.patch.object(watch.subprocess, "run", return_value=result) as run:
            self.assertTrue(watch.query_payload()["success"])
        run.assert_called_once_with(watch.COMMAND, shell=False, stdin=subprocess.DEVNULL,
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                    timeout=30, check=False)
        result.returncode = 1
        result.stderr = b"secret-diagnostic"
        with mock.patch.object(watch.subprocess, "run", return_value=result), self.assertRaises(watch.WatchError):
            watch.query_payload()

    def test_failed_atomic_replace_does_not_emit_or_advance(self):
        self.baseline()
        before = self.path.read_bytes()
        with mock.patch.object(watch, "query_payload", return_value=payload("new")), \
                mock.patch.object(watch.os, "replace", side_effect=OSError("secret-path")):
            code, out, err = self.invoke()
        self.assertEqual((code, out), (1, ""))
        self.assertNotIn("secret", err)
        self.assertEqual(self.path.read_bytes(), before)
        self.assertFalse(list(self.path.parent.glob(".interaction-watch-*")))


if __name__ == "__main__":
    unittest.main()
