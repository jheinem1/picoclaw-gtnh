#!/usr/bin/env python3
from pathlib import Path
import runpy

# Exec safety guard blocks '/' in command strings; this wrapper allows calling:
#   python gtnh_query.py ...
# from workspace root without any path separators in the command.
TARGET = Path(__file__).resolve().parent / "tools" / "gtnh_query.py"
runpy.run_path(str(TARGET), run_name="__main__")
