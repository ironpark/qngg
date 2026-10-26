#!/usr/bin/env python3
"""Generates ffi_bindings.go: purego declarations for every Wasmtime C API
function referenced by the Go sources of the wasmtime package.

Prototypes come from clang's JSON AST of the Wasmtime C API headers, so the Go
signatures always match the headers of the release being bound.

Usage: gen_bindings.py <wasmtime-c-api-include-dir> [package-dir]

Requires clang. Download the headers from the Wasmtime release matching
embedded.Version (the `include` directory of any *-c-api archive).
"""
import json
import os
import re
import subprocess
import sys
import tempfile

# C scalar typedefs and their Go equivalents.
SCALARS = {
    "void": None,
    "bool": "bool",
    "size_t": "uintptr",
    "int": "int32",
    "int32_t": "int32",
    "int64_t": "int64",
    "uint8_t": "uint8",
    "uint32_t": "uint32",
    "uint64_t": "uint64",
    "float": "float32",
    "double": "float64",
    "float32_t": "float32",
    "float64_t": "float64",
    "wasm_memory_pages_t": "uint32",
    "wasm_table_size_t": "uint32",
    "wasm_externkind_t": "uint8",
    "wasm_valkind_t": "wasm_valkind_t",
    "wasm_mutability_t": "wasm_mutability_t",
    "wasmtime_strategy_t": "wasmtime_strategy_t",
    "wasmtime_opt_level_t": "wasmtime_opt_level_t",
    "wasmtime_profiling_strategy_t": "wasmtime_profiling_strategy_t",
    "wasmtime_regalloc_algorithm_t": "uint8",
    "wasmtime_trap_code_t": "uint8",
}

# Pointee types that are plain bytes.
BYTES = {"uint8_t", "byte_t", "wasm_byte_t", "char"}

# Parameters that must be nullable C strings, so they are passed as *byte
# (see cString in ffi.go) instead of a Go string.
NULLABLE_STRINGS = {("wasmtime_config_cache_config_load", 1)}

# Typedef aliases of struct types declared in ffi.go.
ALIASES = {"wasm_name_t": "wasm_byte_vec_t", "wasm_message_t": "wasm_byte_vec_t"}


def parse_headers(include):
    with tempfile.NamedTemporaryFile("w", suffix=".c", delete=False) as f:
        f.write("#include <wasmtime.h>\n")
        src = f.name
    try:
        out = subprocess.run(
            ["clang", "-Xclang", "-ast-dump=json", "-fsyntax-only", "-I", include, src],
            check=True, capture_output=True,
        ).stdout
    finally:
        os.unlink(src)

    fns = {}
    for node in json.loads(out)["inner"]:
        if node.get("kind") != "FunctionDecl":
            continue
        name = node["name"]
        if not name.startswith(("wasm", "wasi")):
            continue
        if node.get("storageClass") == "static" or node.get("inline"):
            continue
        params = [
            p["type"]["qualType"]
            for p in node.get("inner", [])
            if p["kind"] == "ParmVarDecl"
        ]
        ret = node["type"]["qualType"].split("(")[0].strip()
        fns[name] = (ret, params)
    return fns


def go_type(c, next_param=None):
    c = c.replace("const ", "").replace("struct ", "").strip()
    if "(*)" in c or c.endswith("_callback_t") or c.endswith("_destructor_t"):
        return "uintptr"  # function pointers are passed as callback addresses
    c = re.sub(r"\s*\*\s*const\s*\*", " **", c)
    c = re.sub(r"\s+", " ", c).replace(" *", "*").strip()
    stars = len(c) - len(c.rstrip("*"))
    base = c.rstrip("*").strip()
    base = ALIASES.get(base, base)
    if stars == 0:
        if base in SCALARS:
            return SCALARS[base]
        return base  # struct passed by value
    if base == "void":
        return "uintptr" if stars == 1 else "*" * (stars - 1) + "uintptr"
    if base == "char" and stars == 1:
        # `const char *` paired with a length is a byte buffer, otherwise it is
        # a null-terminated string that purego converts for us.
        return "*byte" if next_param == "size_t" else "string"
    if base in BYTES:
        return "*" * stars + "byte"
    if base in SCALARS:
        return "*" * stars + SCALARS[base]
    return "*" * stars + base


def go_signature(name, ret, params):
    args = []
    for i, p in enumerate(params):
        nxt = params[i + 1].replace("const ", "").strip() if i + 1 < len(params) else None
        t = "*byte" if (name, i) in NULLABLE_STRINGS else go_type(p, nxt)
        args.append(f"a{i} {t}")
    r = go_type(ret)
    return f"func({', '.join(args)})" + (f" {r}" if r else "")


def main():
    include = sys.argv[1]
    pkg = sys.argv[2] if len(sys.argv) > 2 else os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    fns = parse_headers(include)

    used = set()
    ident = re.compile(r"\b(wasm[a-z0-9_]*|wasi[a-z0-9_]*)\b")
    for name in sorted(os.listdir(pkg)):
        if not name.endswith(".go") or name == "ffi_bindings.go":
            continue
        with open(os.path.join(pkg, name)) as f:
            src = f.read()
        # Drop comments and string literals so documentation and symbol names
        # bound by hand do not pull in bindings.
        src = re.sub(r"//.*", "", src)
        src = re.sub(r'"(?:[^"\\\n]|\\.)*"', '""', src)
        used |= {m for m in ident.findall(src) if m in fns}

    lines = [
        "// Code generated by gen/gen_bindings.py; DO NOT EDIT.",
        "",
        "package wasmtime",
        "",
        "var (",
    ]
    for name in sorted(used):
        lines.append(f"\t{name} {go_signature(name, *fns[name])}")
    lines += ["", ")", "", "var ("]
    for name in sorted(used):
        lines.append(f'\t_ = bind(&{name}, "{name}")')
    lines += [")", ""]

    out = os.path.join(pkg, "ffi_bindings.go")
    with open(out, "w") as f:
        f.write("\n".join(lines))
    subprocess.run(["gofmt", "-w", out], check=True)
    print(f"bound {len(used)} functions")


if __name__ == "__main__":
    main()
