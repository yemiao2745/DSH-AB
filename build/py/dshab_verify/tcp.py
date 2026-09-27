"""端口：可用性探测与占位监听，全部走 socket。

旧工具链用 TcpListener；这里用 socket，安装器内部的探测也是同一件事（bind 127.0.0.1）。
"""

from __future__ import annotations

import socket

LOOPBACK = "127.0.0.1"


def is_free(port, host=LOOPBACK, timeout=1.0):
    """能否在 host:port 上绑定成功——与安装器的判据一致。"""
    probe = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    try:
        probe.settimeout(timeout)
        probe.bind((host, port))
        return True
    except OSError:
        return False
    finally:
        probe.close()


class PortHolder:
    """占住 127.0.0.1:port，用来验证「端口被占用必须被拒绝」。"""

    def __init__(self, port, host=LOOPBACK):
        self.port = port
        self.host = host
        self._socket = None

    def start(self):
        if self._socket is not None:
            raise RuntimeError(f"端口已经占住了：{self.host}:{self.port}")
        holder = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        holder.bind((self.host, self.port))
        holder.listen(1)
        self._socket = holder
        return self

    def stop(self):
        if self._socket is not None:
            self._socket.close()
            self._socket = None

    def __enter__(self):
        return self.start()

    def __exit__(self, *_exc):
        self.stop()
