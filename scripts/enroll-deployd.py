#!/usr/bin/env python3
"""Repository entry point; production image packages backend/deploy directly."""
from pathlib import Path
import runpy
_impl = runpy.run_path(str(Path(__file__).resolve().parents[1] / "backend/deploy/enroll-deployd.py"))
prepare = _impl["prepare"]
enroll_graphics = _impl["enroll_graphics"]
enroll_operations = _impl["enroll_operations"]
migrate_application_paths = _impl["migrate_application_paths"]
if __name__ == "__main__":
    _impl["main"]()
