"""Cobertura de contrato: cuantas operaciones de openapi.yaml obtuvieron al menos
una respuesta 2xx real (camino feliz) en la corrida de Schemathesis.

Uso: coverage.py openapi.yaml reporte.har [--min-ok=N]
Lee el HAR que genera `schemathesis run --report har`, empareja cada request con
una operacion del contrato (metodo + ruta con parametros) y muestra una tabla.
Sale con 1 si alguna operacion no tuvo ningun 2xx y no esta en EXPECTED_NO_2XX.
"""

import json
import re
import sys
from urllib.parse import urlparse

import yaml

# Operaciones que por diseno no pueden dar 2xx en la corrida (motivo en el script).
EXPECTED_NO_2XX = {
    # Excluida del fuzzing: con un token de forma valida el servicio llama a Google.
    "POST /v1/oauth/google",
}

METHODS = {"get", "put", "post", "delete", "patch", "options", "head", "trace"}


def load_operations(path):
    with open(path, encoding="utf-8") as f:
        spec = yaml.safe_load(f)
    base = ""
    ops = []
    for tpl, item in spec.get("paths", {}).items():
        regex = re.compile("^" + re.sub(r"\{[^}/]+\}", r"[^/]+", re.escape(tpl).replace(r"\{", "{").replace(r"\}", "}")) + "$")
        for method in item:
            if method.lower() in METHODS:
                ops.append((method.upper(), tpl, regex))
    return base, ops


def main():
    spec_path, har_paths = sys.argv[1], sys.argv[2:]
    _, ops = load_operations(spec_path)
    entries = []
    for har_path in har_paths:
        with open(har_path, encoding="utf-8") as f:
            entries.extend(json.load(f)["log"]["entries"])

    touched = {}
    ok = {}
    for e in entries:
        method = e["request"]["method"].upper()
        path = urlparse(e["request"]["url"]).path
        status = e["response"]["status"]
        for m, tpl, regex in ops:
            if m == method and regex.match(path):
                key = (m, tpl)
                touched[key] = touched.get(key, 0) + 1
                if 200 <= status < 300:
                    ok[key] = ok.get(key, 0) + 1
                break

    total = len(ops)
    n_touched = len([1 for m, t, _ in ops if (m, t) in touched])
    n_ok = len([1 for m, t, _ in ops if (m, t) in ok])
    print(f"Cobertura de contrato: tocadas {n_touched}/{total}, con 2xx real {n_ok}/{total}")
    missing = []
    for m, tpl, _ in ops:
        key = (m, tpl)
        mark = "OK " if key in ok else "---"
        print(f"  {mark} {m:6} {tpl}  requests={touched.get(key, 0)} 2xx={ok.get(key, 0)}")
        if key not in ok and f"{m} {tpl}" not in EXPECTED_NO_2XX:
            missing.append(f"{m} {tpl}")
    if missing:
        print("Sin ningun 2xx:", ", ".join(missing), file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
