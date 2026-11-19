"""Escenario de camino feliz para el test de contrato de identify.

Schemathesis, con datos al azar, nunca llega al 2xx de los flujos con estado
(registro, confirmacion, login, refresh, logout, PIN, 2FA, recupero). Este
escenario los recorre con datos validos contra el servicio real y valida cada
respuesta contra openapi.yaml con la misma libreria (status, esquema,
content-type, cabeceras). Escribe un HAR minimo para medir la cobertura.

Uso: scenario.py <openapi.yaml> <base_url> <directorio_de_mails> <har_de_salida>
Solo usa valores de juguete y el SMTP falso local.
"""

import base64
import hashlib
import hmac
import json
import os
import re
import struct
import sys
import time
import uuid

import schemathesis

spec_path, BASE, MAIL_DIR, HAR_OUT = sys.argv[1:5]
SCHEMA = schemathesis.openapi.from_path(spec_path)

RUN = uuid.uuid4().hex[:10]
EMAIL = f"scenario-{RUN}@example.test"
PASSWORD = "Contract-Pass1"
NEW_PASSWORD = "Contract-Pass2"
CHANGED_PASSWORD = "Contract-Pass3"
DEVICE = f"device-{RUN}"
PIN = "123456"

entries = []
failures = []


def call(method, path, expect, body=None, bearer=None, cookies=None):
    """Hace la llamada, valida la respuesta contra el contrato y la registra."""
    kwargs = {}
    if body is not None:
        kwargs["body"] = body
    if bearer:
        kwargs["headers"] = {"Authorization": f"Bearer {bearer}"}
    if cookies:
        kwargs["cookies"] = cookies
    case = SCHEMA[path][method].Case(**kwargs)
    response = case.call(base_url=BASE)
    entries.append(
        {
            "request": {"method": method, "url": BASE + path},
            "response": {"status": response.status_code},
        }
    )
    if response.status_code != expect:
        failures.append(f"{method} {path}: se esperaba {expect} y llego {response.status_code}: {response.text[:200]}")
        return response
    try:
        case.validate_response(response)
    except Exception as exc:  # FailureGroup con el detalle de cada check
        failures.append(f"{method} {path}: la respuesta no cumple el contrato: {exc}")
    return response


def mail_token(rcpt, timeout=15):
    """Lee el token del ultimo mail enviado a rcpt (SMTP falso)."""
    path = f"{MAIL_DIR}/{rcpt}.txt"
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with open(path, encoding="utf-8") as f:
                match = re.search(r"token=([0-9a-fA-F]{20,})", f.read())
            if match:
                return match.group(1)
        except FileNotFoundError:
            pass
        time.sleep(0.3)
    raise SystemExit(f"scenario: no llego el mail a {rcpt}")


def totp(secret_b32, offset=0):
    key = base64.b32decode(secret_b32.upper() + "=" * (-len(secret_b32) % 8))
    counter = int(time.time() // 30) + offset
    digest = hmac.new(key, struct.pack(">Q", counter), hashlib.sha1).digest()
    pos = digest[-1] & 0x0F
    code = (struct.unpack(">I", digest[pos : pos + 4])[0] & 0x7FFFFFFF) % 10**6
    return f"{code:06d}"


def refresh_cookie(token):
    return {"refresh_token": token}


def refresh_from(response):
    """El refresh token viaja en la cookie httpOnly, no en el cuerpo."""
    match = re.search(r"refresh_token=([^;,\s]+)", " ".join(response.headers.get("set-cookie", [])))
    if not match:
        failures.append(f"la respuesta de {response.request.url} no trae la cookie refresh_token")
        raise SystemExit("scenario: sin cookie de refresh")
    return match.group(1)


def main():
    # Registro y confirmacion de email.
    call("POST", "/v1/users", 201, {"name": "Scenario User", "email": EMAIL, "password": PASSWORD})
    call("POST", "/v1/email-confirmation/confirm", 200, {"token": mail_token(EMAIL)})

    # Login, refresh (rota el token) y logout.
    response = call("POST", "/v1/login", 200, {"email": EMAIL, "password": PASSWORD})
    token, refresh = response.json()["token"], refresh_from(response)
    response = call("POST", "/v1/refresh", 200, cookies=refresh_cookie(refresh))
    token, refresh = response.json()["token"], refresh_from(response)

    # PIN: configurar y entrar con el.
    call("POST", "/v1/pin", 204, {"device_id": DEVICE, "pin": PIN}, bearer=token)
    call("POST", "/v1/pin/login", 200, {"device_id": DEVICE, "pin": PIN})

    # 2FA: setup, activacion con TOTP, regenerar codigos, login con desafio, recupero y baja.
    setup = call("POST", "/v1/me/2fa/setup", 200, bearer=token).json()
    activated = call("POST", "/v1/me/2fa/activate", 200, {"code": totp(setup["secret"])}, bearer=token).json()
    challenge = call("POST", "/v1/login", 202, {"email": EMAIL, "password": PASSWORD}).json()["challenge_token"]
    call("POST", "/v1/login/2fa", 200, {"challenge_token": challenge, "code": activated["recovery_codes"][0]})
    challenge = call("POST", "/v1/login", 202, {"email": EMAIL, "password": PASSWORD}).json()["challenge_token"]
    call("POST", "/v1/login/2fa/recovery-request", 201, {"challenge_token": challenge, "message": "Perdi el acceso"})
    call("POST", "/v1/me/2fa/recovery-codes", 200, {"password": PASSWORD}, bearer=token)
    call("POST", "/v1/me/2fa/disable", 204, {"password": PASSWORD}, bearer=token)

    # Recupero de contrasena por mail.
    call("POST", "/v1/password-recovery", 202, {"email": EMAIL})
    call(
        "POST",
        "/v1/password-reset",
        200,
        {"token": mail_token(EMAIL), "new_password": NEW_PASSWORD, "confirm_password": NEW_PASSWORD},
    )
    response = call("POST", "/v1/login", 200, {"email": EMAIL, "password": NEW_PASSWORD})
    token, refresh = response.json()["token"], refresh_from(response)

    # Cambio de contrasena con la actual y login con la nueva.
    call(
        "PUT",
        "/v1/me/password",
        204,
        {"current_password": NEW_PASSWORD, "new_password": CHANGED_PASSWORD, "confirm_password": CHANGED_PASSWORD},
        bearer=token,
    )
    call("POST", "/v1/login", 200, {"email": EMAIL, "password": CHANGED_PASSWORD})

    # Cuenta sin contrasena (la del usuario sembrado, como las de Google): fijar una.
    call("POST", "/v1/me/password", 204, {"password": PASSWORD}, bearer=os.environ["CONTRACT_USER_TOKEN"])

    # Logout con la cookie de refresh.
    call("POST", "/v1/logout", 204, bearer=token, cookies=refresh_cookie(refresh))


try:
    main()
finally:
    with open(HAR_OUT, "w", encoding="utf-8") as f:
        json.dump({"log": {"entries": entries}}, f)

if failures:
    print("scenario: fallas en el camino feliz:", file=sys.stderr)
    for failure in failures:
        print("  - " + failure, file=sys.stderr)
    sys.exit(1)
print(f"scenario: {len(entries)} llamadas validadas contra el contrato")
