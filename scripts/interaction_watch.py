#!/usr/bin/env python3
"""One bounded, read-only notices snapshot poll; no scheduler or messaging.

Unknown item schemas are deliberately not interpreted. Only top-level has_more
(when boolean) and attach_info (when a string pagination cursor) are excluded;
unknown types and all other data are hashed conservatively.
A change is not proof of a new interaction, and first-page snapshots can miss
changes outside that page. subprocess.run captures output before enforcing the
2 MiB limit, so that limit is a parser bound, not a process memory bound.

API: process_payload(payload, state=None) -> (new_state, notice_or_None).
State: {version: 1, digest: SHA256, pending_digest: SHA256 | None}.
A pending notice must be delivered then acknowledged via --ack DELIVERY_ID;
repeated polls return it without invoking the CLI until acknowledgment succeeds.
Filesystem checks reject observed symlinks, including directory components, but
are not a sandbox against concurrent replacement of ancestor directories by an
attacker with directory write access. Use a trusted, dedicated state directory.
"""

import argparse
from contextlib import contextmanager
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile

MAX_JSON_BYTES = 2 * 1024 * 1024
MAX_STATE_BYTES = 1024
COMMAND = ("tencent-channel-cli", "feed", "get-notices", "--page-num", "20", "--json")
MESSAGE = "腾讯频道互动通知查询结果有更新，可要求我查看详情。"
ERROR_MESSAGE = "互动通知查询或状态处理失败；请重试并检查状态。"
HEX_DIGEST = re.compile(r"[0-9a-f]{64}\Z")


class WatchError(Exception):
    """An intentionally detail-free error safe to report externally."""


def _reject_constant(_value):
    raise WatchError()


def _unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise WatchError()
        result[key] = value
    return result


def parse_json(raw, maximum=MAX_JSON_BYTES):
    if not isinstance(raw, bytes) or not raw or len(raw) > maximum:
        raise WatchError()
    try:
        return json.loads(raw.decode("utf-8"), parse_constant=_reject_constant,
                          object_pairs_hook=_unique_object)
    except (ValueError, UnicodeError, RecursionError) as exc:
        raise WatchError() from exc


def validate_state(state):
    if not isinstance(state, dict) or set(state) != {"version", "digest", "pending_digest"}:
        raise WatchError()
    if type(state["version"]) is not int or state["version"] != 1:
        raise WatchError()
    for key in ("digest", "pending_digest"):
        value = state[key]
        if key == "pending_digest" and value is None:
            continue
        if not isinstance(value, str) or not HEX_DIGEST.fullmatch(value):
            raise WatchError()
    if state["pending_digest"] == state["digest"]:
        raise WatchError()
    return state


def notice_for(digest):
    return {"status": "changed", "message": MESSAGE, "delivery_id": digest}


def process_payload(payload, state=None):
    """Pure transition; caller must persist returned state before emitting notice."""
    if state is not None:
        validate_state(state)
        if state["pending_digest"] is not None:
            return dict(state), notice_for(state["pending_digest"])
    if not isinstance(payload, dict) or payload.get("success") is not True:
        raise WatchError()
    data = payload.get("data")
    # {} / [] are ambiguous, not evidence of an empty notices result. The real
    # observed {has_more: false} envelope is nonempty and explicitly accepted.
    if not isinstance(data, (dict, list)) or not data:
        raise WatchError()
    snapshot = data
    if isinstance(data, dict):
        snapshot = dict(data)
        if type(snapshot.get("has_more")) is bool:
            snapshot.pop("has_more")
        if isinstance(snapshot.get("attach_info"), str):
            snapshot.pop("attach_info")
    try:
        canonical = json.dumps(snapshot, sort_keys=True, ensure_ascii=True,
                               separators=(",", ":"), allow_nan=False).encode("ascii")
    except (TypeError, ValueError, RecursionError) as exc:
        raise WatchError() from exc
    digest = hashlib.sha256(canonical).hexdigest()
    if state is None:
        return {"version": 1, "digest": digest, "pending_digest": None}, None
    updated = dict(state)
    if digest == state["digest"]:
        return updated, None
    updated["pending_digest"] = digest
    return updated, notice_for(digest)


def acknowledge(state, delivery_id):
    validate_state(state)
    if not isinstance(delivery_id, str) or not HEX_DIGEST.fullmatch(delivery_id):
        raise WatchError()
    if state["pending_digest"] is None or state["pending_digest"] != delivery_id:
        raise WatchError()
    return {"version": 1, "digest": delivery_id, "pending_digest": None}


def _prepare_path(path):
    path = Path(os.path.abspath(os.fspath(path)))
    if path.name in {"", ".", ".."}:
        raise WatchError()
    # Never change permissions on preexisting directories, broad or otherwise.
    parts = list(reversed(path.parent.parents)) + [path.parent]
    for directory in parts:
        try:
            directory.mkdir(mode=0o700)
        except FileExistsError:
            pass
        if not stat.S_ISDIR(directory.lstat().st_mode):
            raise WatchError()
    return path


def _check_regular(fd):
    info = os.fstat(fd)
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_uid != os.getuid():
        raise WatchError()
    if stat.S_IMODE(info.st_mode) != 0o600:
        raise WatchError()


@contextmanager
def locked_state(path):
    path = _prepare_path(path)
    lock_path = path.with_name(path.name + ".lock")
    fd = os.open(lock_path, os.O_WRONLY | os.O_CREAT | os.O_NOFOLLOW | os.O_NONBLOCK, 0o600)
    try:
        _check_regular(fd)
        try:
            # Bounded: concurrent invocation fails safely rather than waiting.
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as exc:
            raise WatchError() from exc
        yield path
    finally:
        os.close(fd)


def load_state(path):
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    except FileNotFoundError:
        return None
    with os.fdopen(fd, "rb") as handle:
        _check_regular(handle.fileno())
        return validate_state(parse_json(handle.read(MAX_STATE_BYTES + 1), MAX_STATE_BYTES))


def save_state(path, state):
    validate_state(state)
    # Recheck destination before replace (does not follow a symlink).
    if path.is_symlink():
        raise WatchError()
    fd, temporary = tempfile.mkstemp(prefix=".interaction-watch-", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as handle:
            os.fchmod(handle.fileno(), 0o600)
            handle.write((json.dumps(state, sort_keys=True) + "\n").encode("ascii"))
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
        directory_fd = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def query_payload():
    result = subprocess.run(COMMAND, shell=False, stdin=subprocess.DEVNULL,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30,
                            check=False)
    if result.returncode != 0:
        raise WatchError()
    return parse_json(result.stdout)


def run_once(path, ack=None):
    """Persist pending before returning notice; acknowledgment never queries CLI."""
    with locked_state(path) as path:
        state = load_state(path)
        if ack is not None:
            if state is None:
                raise WatchError()
            save_state(path, acknowledge(state, ack))
            return None
        if state is not None and state["pending_digest"] is not None:
            return notice_for(state["pending_digest"])
        updated, notice = process_payload(query_payload(), state)
        if updated != state:
            save_state(path, updated)
        return notice


class SafeParser(argparse.ArgumentParser):
    def error(self, _message):
        raise WatchError()


def main(argv=None):
    parser = SafeParser(description=__doc__)
    parser.add_argument("--state", required=True, help="dedicated state JSON path (not CLI configuration)")
    parser.add_argument("--ack", metavar="DELIVERY_ID", help="commit pending change after confirmed delivery")
    try:
        args = parser.parse_args(argv)
        notice = run_once(args.state, args.ack)
        if notice is not None:
            print(json.dumps(notice, ensure_ascii=False))
        return 0
    except (WatchError, OSError, ValueError, TypeError, RecursionError, subprocess.SubprocessError):
        print(json.dumps({"status": "error", "message": ERROR_MESSAGE}, ensure_ascii=False), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
