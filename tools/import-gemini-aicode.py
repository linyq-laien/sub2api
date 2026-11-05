#!/usr/bin/env python3
"""Convert Antigravity consumer credentials to a Sub2API account import bundle."""
import argparse
import datetime
import json
import os
from pathlib import Path

CLIENT_ID = "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("credentials", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--name", default="Gemini AI Code")
    args = parser.parse_args()
    source = json.loads(args.credentials.read_text())
    if source.get("client_id") != CLIENT_ID:
        parser.error("credentials must belong to the Antigravity consumer OAuth client")
    if not source.get("refresh_token"):
        parser.error("refresh_token is required")
    if "https://www.googleapis.com/auth/aicode" not in source.get("scope", "").split():
        parser.error("credentials must include the auth/aicode scope; sign in with Antigravity first")
    credentials = {key: source[key] for key in ("client_id", "refresh_token", "access_token", "scope") if source.get(key)}
    credentials.update(oauth_type="aicode", expires_at="0", aicode_tier="g1-pro-tier",
                       model_mapping={"gemini-3.5-flash-lite": "gemini-3.5-flash-lite"})
    bundle = {"type": "sub2api-data", "version": 1,
              "exported_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
              "proxies": [], "accounts": [{"name": args.name, "platform": "gemini", "type": "oauth",
                                           "credentials": credentials, "concurrency": 1, "priority": 1}]}
    # Never overwrite an existing file or create a world-readable token bundle.
    fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w") as out:
        json.dump(bundle, out, ensure_ascii=False, indent=2)
        out.write("\n")
    print(f"Created {args.output} (permissions 0600)")


if __name__ == "__main__":
    main()
