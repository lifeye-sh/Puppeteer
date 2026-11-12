"""Compare OpenAPI with `nt routes`; registration is not functional acceptance.

Requires PyYAML. Never connects to a server or reads credentials.
"""
import argparse
import json
from pathlib import Path
import sys
import yaml

METHODS = {"get", "post", "put", "patch", "delete", "head", "options", "trace"}
ROOT = Path(__file__).resolve().parent.parent


def path_item(document, item, cache):
    if "$ref" not in item:
        return item
    filename, pointer = item["$ref"].split("#", 1)
    target = (document.parent / filename).resolve() if filename else document
    if not target.is_relative_to(ROOT / "backend" / "api"):
        raise ValueError("contract reference must remain in backend/api")
    if target not in cache:
        cache[target] = yaml.safe_load(target.read_text(encoding="utf-8"))
    value = cache[target]
    for segment in pointer.lstrip("/").split("/"):
        value = value[segment.replace("~1", "/").replace("~0", "~")]
    if "$ref" in value:
        raise ValueError("nested path references are not supported by this audit")
    return value


def audit(routes_file, output):
    routes = json.loads(routes_file.read_text(encoding="utf-8-sig"))
    registered = {r["method"] + " " + r["path"] for r in routes["routes"] if not r.get("fallback")}
    document = (ROOT / "backend/api/openapi.yaml").resolve()
    root = yaml.safe_load(document.read_text(encoding="utf-8"))
    cache = {document: root}
    documented = set()
    for path, item in root["paths"].items():
        for method in path_item(document, item, cache):
            if method in METHODS:
                documented.add(method.upper() + " " + path)
    result = {
        "release": routes["release"],
        "scope": "Registration versus OpenAPI only; does not prove schemas, permissions, buttons or business acceptance",
        "completeProductAcceptance": False,
        "registeredOperations": len(registered),
        "documentedOperations": len(documented),
        "matchedOperations": len(registered & documented),
        "documentedButUnregistered": sorted(documented - registered),
        "registeredWithoutOpenAPI": sorted(registered - documented),
    }
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return result


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--routes", type=Path, required=True)
    parser.add_argument("--output", type=Path, default=ROOT / "docs/backend/acceptance/R16-route-contract-audit.json")
    parser.add_argument("--require-complete", action="store_true")
    args = parser.parse_args()
    result = audit(args.routes, args.output)
    print(json.dumps({k: v for k, v in result.items() if not isinstance(v, list)}, ensure_ascii=False))
    print("Undocumented operations:", len(result["registeredWithoutOpenAPI"]))
    sys.exit(bool(result["documentedButUnregistered"]) or bool(args.require_complete and result["registeredWithoutOpenAPI"]))
