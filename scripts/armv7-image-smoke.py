#!/usr/bin/env python3
"""Exercise the shipped ARM clients and health-check downloader under QEMU."""

import http.server
import pathlib
import ssl
import subprocess
import sys
import tempfile
import threading
import uuid


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200 if self.path == "/health" else 503)
        self.end_headers()

    do_HEAD = do_GET

    def log_message(self, *args):
        pass


def main():
    image = sys.argv[1]
    container = "portwing-arm-smoke-" + uuid.uuid4().hex
    servers = []
    threads = []
    with tempfile.TemporaryDirectory(prefix="pw-health-") as directory:
        cert = pathlib.Path(directory) / "cert.pem"
        key = pathlib.Path(directory) / "key.pem"
        subprocess.run(
            [
                "openssl",
                "req",
                "-x509",
                "-newkey",
                "rsa:2048",
                "-nodes",
                "-keyout",
                str(key),
                "-out",
                str(cert),
                "-days",
                "1",
                "-subj",
                "/CN=localhost",
            ],
            check=True,
            capture_output=True,
            timeout=20,
        )
        try:
            subprocess.run(
                [
                    "docker",
                    "run",
                    "--detach",
                    "--name",
                    container,
                    "--platform",
                    "linux/arm/v7",
                    "--network",
                    "host",
                    "--entrypoint",
                    "/bin/sh",
                    image,
                    "-c",
                    "sleep 180",
                ],
                check=True,
                capture_output=True,
                timeout=30,
            )

            def execute(*command):
                return subprocess.run(
                    ["docker", "exec", container, *command],
                    capture_output=True,
                    text=True,
                    timeout=20,
                )

            for command in [
                ("/usr/bin/docker", "--version"),
                ("/usr/bin/docker-compose", "version"),
                ("/usr/bin/docker", "compose", "version"),
            ]:
                result = execute(*command)
                if result.returncode:
                    raise RuntimeError(f"{command}: {result.stderr}")
                print(result.stdout.strip())
            result = execute(
                "/bin/sh",
                "-c",
                '. /etc/os-release; test "$ID" = alpine && test "$(id -u)" = 65532',
            )
            if result.returncode:
                raise RuntimeError("image must identify Alpine and run as UID 65532")

            for secure in (False, True):
                server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
                if secure:
                    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
                    context.load_cert_chain(cert, key)
                    server.socket = context.wrap_socket(server.socket, server_side=True)
                servers.append(server)
                thread = threading.Thread(target=server.serve_forever, daemon=True)
                threads.append(thread)
                thread.start()
                scheme = "https" if secure else "http"
                for path, succeeds in [("/health", True), ("/unready", False)]:
                    flags = ["--no-check-certificate"] if secure else []
                    result = execute(
                        "/usr/bin/wget",
                        "-q",
                        *flags,
                        "--spider",
                        f"{scheme}://127.0.0.1:{server.server_port}{path}",
                    )
                    if (result.returncode == 0) != succeeds:
                        raise RuntimeError(
                            f"{scheme} {path}: status {result.returncode}, {result.stderr}"
                        )
                    print(f"verified {scheme} {path}: exit {result.returncode}")
        finally:
            subprocess.run(
                ["docker", "rm", "--force", container],
                capture_output=True,
                timeout=20,
                check=False,
            )
            for server in servers:
                server.shutdown()
                server.server_close()
            for thread in threads:
                thread.join(timeout=5)


if __name__ == "__main__":
    main()
