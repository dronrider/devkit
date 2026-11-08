#!/usr/bin/env python3
"""Публикация полного снимка журнала quota-refresh под замком вызывающего хука."""
import os
from pathlib import Path
import sys
import tempfile


def report_error(state, phase):
    try:
        (state / "quota-refresh.error").write_text(
            "ошибка записи журнала: %s\n" % phase, encoding="utf-8")
    except OSError:
        pass


def cap_lines(data, keep):
    if keep == 0:
        return b""
    count = keep + int(data.endswith(b"\n"))
    return b"\n".join(data.rsplit(b"\n", count)[-count:])


def publish(state, record, limit, keep):
    log = state / "quota-refresh.log"
    phase = "подготовка"
    try:
        with tempfile.TemporaryDirectory(prefix="quota-refresh.", dir=state) as directory:
            staged = Path(directory) / "journal"
            phase = "чтение"
            previous = log.read_bytes() if log.exists() else b""
            phase = "запись"
            staged.write_bytes(previous + record)
            phase = "размер"
            size = staged.stat().st_size
            if size > limit:
                phase = "обрезка"
                staged.write_bytes(cap_lines(staged.read_bytes(), keep))
            phase = "публикация"
            os.replace(staged, log)
        (state / "quota-refresh.error").unlink(missing_ok=True)
        return 0
    except OSError:
        report_error(state, phase)
        return 1


def main(argv=None):
    args = sys.argv[1:] if argv is None else argv
    state = Path(args[0])
    try:
        code, limit, keep = (int(value) for value in args[1:])
        if limit < 0 or keep < 0:
            raise ValueError("negative limit")
    except ValueError:
        report_error(state, "подготовка")
        return 1
    try:
        record = sys.stdin.buffer.read()
    except OSError:
        report_error(state, "запись")
        return 1
    if not record.endswith(("код возврата: %d\n" % code).encode("utf-8")):
        report_error(state, "запись")
        return 1
    return publish(state, record, limit, keep)


if __name__ == "__main__":
    sys.exit(main())
