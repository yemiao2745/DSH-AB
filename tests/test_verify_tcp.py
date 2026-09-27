"""Unit tests for the port probes (build/py/dshab_verify/tcp.py).

Contract: BUILD_CONTRACT.md section 4, scenario 4 - the acceptance run must be able to prove that a
taken production port really is taken (is_free sees the holder) and that the installer is then
refused. Everything here stays on the loopback interface and talks to no peer.
"""

from __future__ import annotations

import socket
import sys
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
BUILD_PY = REPO_ROOT / "build" / "py"
if str(BUILD_PY) not in sys.path:
    sys.path.insert(0, str(BUILD_PY))

from dshab_verify import tcp  # noqa: E402

ATTEMPTS = 10


def ephemeral_port():
    """A port the kernel just handed out and that nothing holds any more."""
    probe = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    try:
        probe.bind((tcp.LOOPBACK, 0))
        return probe.getsockname()[1]
    finally:
        probe.close()


def hold_a_free_port():
    """A started holder on a port that was free a moment ago; retries because the port can be taken."""
    for _ in range(ATTEMPTS):
        holder = tcp.PortHolder(ephemeral_port())
        try:
            return holder.start()
        except OSError:
            continue
    raise unittest.SkipTest("本机找不到可占用的空闲端口")


class FreePortTests(unittest.TestCase):
    def test_the_probe_uses_the_loopback_interface(self):
        self.assertEqual(tcp.LOOPBACK, "127.0.0.1")

    def test_an_unused_ephemeral_port_is_free(self):
        self.assertTrue(tcp.is_free(ephemeral_port()))

    def test_a_held_port_is_not_free(self):
        holder = hold_a_free_port()
        try:
            self.assertFalse(tcp.is_free(holder.port))
            self.assertFalse(tcp.is_free(holder.port, tcp.LOOPBACK))
        finally:
            holder.stop()

    def test_the_context_manager_releases_the_port(self):
        for _ in range(ATTEMPTS):
            port = ephemeral_port()
            holder = tcp.PortHolder(port)
            try:
                with holder as started:
                    self.assertIs(started, holder)
                    self.assertFalse(tcp.is_free(port))
            except OSError:
                continue  # the port was taken between the probe and the bind
            self.assertTrue(tcp.is_free(port))
            return
        self.skipTest("本机找不到可占用的空闲端口")

    def test_stop_releases_the_port_and_is_idempotent(self):
        holder = hold_a_free_port()
        port = holder.port
        self.assertFalse(tcp.is_free(port))
        holder.stop()
        holder.stop()
        self.assertTrue(tcp.is_free(port))

    def test_starting_a_started_holder_is_an_error(self):
        holder = hold_a_free_port()
        try:
            with self.assertRaises(RuntimeError):
                holder.start()
        finally:
            holder.stop()


if __name__ == "__main__":
    unittest.main()
