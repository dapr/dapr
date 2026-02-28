#!/usr/bin/env python3
"""Render test YAML files by substituting a small set of placeholders.

Supported placeholders:
  - ${DAPR_TEST_NAMESPACE}
  - ${DAPR_TEST_ENV_NAMESPACE}

We intentionally do not implement full envsubst semantics; only these
placeholders are replaced.
"""

from __future__ import annotations

import os
import sys

ALLOWED_VARS = ("DAPR_TEST_NAMESPACE", "DAPR_TEST_ENV_NAMESPACE")


def main() -> int:
    if len(sys.argv) != 2:
        print(f"Usage: {sys.argv[0]} <yaml-file>", file=sys.stderr)
        return 2

    path = sys.argv[1]
    with open(path, "r", encoding="utf-8") as f:
        content = f.read()

    for var in ALLOWED_VARS:
        content = content.replace("${" + var + "}", os.environ.get(var, ""))

    sys.stdout.write(content)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
