#!/usr/bin/env python3
"""Exercise the shipped ARM clients and shell-free healthcheck under QEMU."""

import http.server
import pathlib
import ssl
import subprocess
import sys
import tempfile
import threading
import uuid


class Handler(http.server.BaseHTTPRequestHandler):
    status = 200

    def do_GET(self):
        self.send_response(self.status if self.path == "/health" else 404)
        self.end_headers()

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
            ["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
             "-keyout", str(key), "-out", str(cert), "-days", "1",
             "-subj", "/CN=localhost"],
            check=True, capture_output=True, timeout=20,
        )
        try:
            # Inspect the exact filesystem and user without assuming any shell
            # or helper applets exist in the runtime image.
            subprocess.run(
                ["docker", "create", "--name", container, "--platform",
                 "linux/arm/v7", image, "version"],
                check=True, capture_output=True, timeout=30,
            )
            user = subprocess.run(
                ["docker", "inspect", "--format", "{{.Config.User}}", container],
                check=True, capture_output=True, text=True, timeout=20,
            ).stdout.strip()
            for remote, local in [("/etc/os-release", "os-release"),
                                  ("/lib/apk/db/installed", "installed")]:
                subprocess.run(
                    ["docker", "cp", "-L", f"{container}:{remote}",
                     str(pathlib.Path(directory) / local)],
                    check=True, capture_output=True, timeout=20,
                )
            distro = (pathlib.Path(directory) / "os-release").read_text()
            packages = (pathlib.Path(directory) / "installed").read_text()
            if user != "65532:65532" or "ID=alpine" not in distro.splitlines():
                raise RuntimeError("image must identify Alpine and run as UID 65532")
            if any(line in packages.splitlines() for line in
                   ["P:busybox", "P:busybox-binsh", "P:ssl_client"]):
                raise RuntimeError("ARM runtime must not retain BusyBox packages")
            subprocess.run(["docker", "rm", container], check=True,
                           capture_output=True, timeout=20)

            def execute(command, *arguments, environment=()):
                env_args = [item for value in environment for item in ("-e", value)]
                return subprocess.run(
                    ["docker", "run", "--rm", "--name", container,
                     "--platform", "linux/arm/v7", "--network", "host",
                     *env_args, "--entrypoint", command, image, *arguments],
                    capture_output=True, text=True, timeout=30,
                )

            for command in [("/usr/bin/docker", "--version"),
                            ("/usr/bin/docker-compose", "version"),
                            ("/usr/bin/docker", "compose", "version")]:
                result = execute(*command)
                if result.returncode:
                    raise RuntimeError(f"{command}: {result.stderr}")
                print(result.stdout.strip())

            for secure in (False, True):
                for status in (200, 503):
                    handler = type("HealthHandler", (Handler,), {"status": status})
                    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
                    if secure:
                        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
                        context.load_cert_chain(cert, key)
                        server.socket = context.wrap_socket(server.socket, server_side=True)
                    servers.append(server)
                    thread = threading.Thread(target=server.serve_forever, daemon=True)
                    threads.append(thread)
                    thread.start()
                    result = execute(
                        "/usr/bin/portwing", "healthcheck",
                        environment=(f"PORT={server.server_port}",
                                     "TLS_CERT=self-signed" if secure else "TLS_CERT=",
                                     "HTTP_PROXY=http://127.0.0.1:1",
                                     "HTTPS_PROXY=http://127.0.0.1:1"),
                    )
                    if (result.returncode == 0) != (status == 200):
                        raise RuntimeError(f"TLS={secure} HTTP={status}: {result.stderr}")
                    print(f"verified TLS={secure} HTTP={status}: exit {result.returncode}")
        finally:
            subprocess.run(["docker", "rm", "--force", container],
                           capture_output=True, timeout=20, check=False)
            for server in servers:
                server.shutdown()
                server.server_close()
            for thread in threads:
                thread.join(timeout=5)


if __name__ == "__main__":
    main()
