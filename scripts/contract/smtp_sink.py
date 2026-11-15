#!/usr/bin/env python3
"""Servidor SMTP falso para los tests de contrato (solo biblioteca estandar).

Escucha solo en 127.0.0.1, acepta cualquier mail y lo descarta: nada sale a
internet. Uso: smtp_sink.py <puerto>
"""
import socketserver
import sys


class Handler(socketserver.StreamRequestHandler):
    def reply(self, line: str) -> None:
        self.wfile.write((line + "\r\n").encode())

    def handle(self) -> None:
        self.reply("220 contract-smtp-sink ready")
        in_data = False
        while True:
            raw = self.rfile.readline()
            if not raw:
                return
            line = raw.decode("utf-8", "replace").rstrip("\r\n")
            if in_data:
                if line == ".":
                    in_data = False
                    self.reply("250 queued")
                continue
            cmd = line.upper()
            if cmd.startswith("EHLO"):
                self.reply("250 contract-smtp-sink")
            elif cmd.startswith("DATA"):
                in_data = True
                self.reply("354 end with <CRLF>.<CRLF>")
            elif cmd.startswith("QUIT"):
                self.reply("221 bye")
                return
            else:
                # HELO, MAIL FROM, RCPT TO, RSET, NOOP...
                self.reply("250 ok")


class Server(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


def main() -> int:
    if len(sys.argv) != 2:
        print("uso: smtp_sink.py <puerto>", file=sys.stderr)
        return 2
    with Server(("127.0.0.1", int(sys.argv[1])), Handler) as srv:
        srv.serve_forever()
    return 0


if __name__ == "__main__":
    sys.exit(main())
