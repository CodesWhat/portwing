#!/usr/bin/env python3
"""Exercise static runtime clients and the shell-free HTTP/TLS healthcheck."""

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
    platform = sys.argv[2] if len(sys.argv) > 2 else "linux/arm/v7"
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
        cert.chmod(0o644)
        try:
            # Inspect the exact filesystem and user without assuming any shell
            # or helper applets exist in the runtime image.
            subprocess.run(
                ["docker", "create", "--name", container, "--platform",
                 platform, image, "version"],
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
                   ["P:busybox", "P:busybox-binsh", "P:ssl_client", "P:glibc", "P:musl"]):
                raise RuntimeError("static runtime must not retain shell or libc packages")
            for client in ("portwing", "docker", "docker-compose"):
                binary = pathlib.Path(directory) / client
                subprocess.run(
                    ["docker", "cp", f"{container}:/usr/bin/{client}", str(binary)],
                    check=True, capture_output=True, timeout=30,
                )
                linkage = subprocess.run(
                    ["file", "-b", str(binary)], check=True,
                    capture_output=True, text=True, timeout=10,
                ).stdout
                if "statically linked" not in linkage:
                    raise RuntimeError(f"{client} must be statically linked: {linkage}")
                print(f"verified {client}: {linkage.strip()}")
            subprocess.run(["docker", "rm", container], check=True,
                           capture_output=True, timeout=20)

            def execute(command, *arguments, environment=(), certificate=None):
                env_args = [item for value in environment for item in ("-e", value)]
                cert_args = (["--mount",
                              f"type=bind,source={certificate},target=/tmp/health-cert.pem,readonly"]
                             if certificate else [])
                return subprocess.run(
                    ["docker", "run", "--rm", "--name", container,
                     "--platform", platform, "--network", "host",
                     "--read-only", "--cap-drop=ALL",
                     "--security-opt=no-new-privileges", "--tmpfs", "/tmp",
                     *env_args, *cert_args, "--entrypoint", command, image, *arguments],
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
                                     "TLS_CERT=/tmp/health-cert.pem" if secure else "TLS_CERT=",
                                     "HTTP_PROXY=http://127.0.0.1:1",
                                     "HTTPS_PROXY=http://127.0.0.1:1"),
                        certificate=cert if secure else None,
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
