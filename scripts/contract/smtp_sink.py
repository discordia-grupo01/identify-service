#!/usr/bin/env python3
"""Servidor SMTP falso para los tests de contrato (solo biblioteca estandar).

Escucha solo en 127.0.0.1 y acepta cualquier mail: nada sale a internet.
Uso: smtp_sink.py <puerto> [directorio]
Con directorio, guarda el ultimo mail de cada destinatario como <destinatario>.txt
(texto ya decodificado) para que el escenario de contrato lea los tokens de
confirmacion y de recupero. Sin directorio, los descarta.
"""
import email
import email.policy
import os
import re
import socketserver
import sys

OUT_DIR = None


def save(rcpt: str, raw: bytes) -> None:
    if not OUT_DIR or not rcpt:
        return
    msg = email.message_from_bytes(raw, policy=email.policy.default)
    body = msg.get_body(preferencelist=("plain", "html"))
    text = body.get_content() if body is not None else raw.decode("utf-8", "replace")
    name = re.sub(r"[^A-Za-z0-9@._+-]", "_", rcpt)
    with open(os.path.join(OUT_DIR, name + ".txt"), "w", encoding="utf-8") as f:
        f.write(text)


class Handler(socketserver.StreamRequestHandler):
    def reply(self, line: str) -> None:
        self.wfile.write((line + "\r\n").encode())

    def handle(self) -> None:
        self.reply("220 contract-smtp-sink ready")
        in_data = False
        rcpt = ""
        data = []
        while True:
            raw = self.rfile.readline()
            if not raw:
                return
            line = raw.decode("utf-8", "replace").rstrip("\r\n")
            if in_data:
                if line == ".":
                    in_data = False
                    save(rcpt, "\r\n".join(data).encode("utf-8"))
                    data = []
                    self.reply("250 queued")
                else:
                    data.append(line[1:] if line.startswith("..") else line)
                continue
            cmd = line.upper()
            if cmd.startswith("EHLO"):
                self.reply("250 contract-smtp-sink")
            elif cmd.startswith("RCPT TO:"):
                match = re.search(r"<([^>]*)>", line)
                rcpt = match.group(1) if match else ""
                self.reply("250 ok")
            elif cmd.startswith("DATA"):
                in_data = True
                self.reply("354 end with <CRLF>.<CRLF>")
            elif cmd.startswith("QUIT"):
                self.reply("221 bye")
                return
            else:
                # HELO, MAIL FROM, RSET, NOOP...
                self.reply("250 ok")


class Server(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


def main() -> int:
    global OUT_DIR
    if len(sys.argv) not in (2, 3):
        print("uso: smtp_sink.py <puerto> [directorio]", file=sys.stderr)
        return 2
    if len(sys.argv) == 3:
        OUT_DIR = sys.argv[2]
        os.makedirs(OUT_DIR, exist_ok=True)
    with Server(("127.0.0.1", int(sys.argv[1])), Handler) as srv:
        srv.serve_forever()
    return 0


if __name__ == "__main__":
    sys.exit(main())
