#!/usr/bin/env python3
"""
WeKnora admin bootstrap — create the super user and promote to SystemAdmin.

Runs as a one-shot docker-compose service (init-admin) after the app is
healthy. Idempotent: re-running is a no-op when the user already exists and
is already a system admin.

Three steps:
  1. POST /api/v1/auth/register — create the account (skip if it exists).
     WeKnora has no "first user becomes admin" logic, and the promote API
     itself requires an existing SystemAdmin, so…
  2. SQL UPDATE users SET is_system_admin=true — done over a direct
     postgres connection (instant effect, no WeKnora restart needed).
  3. POST /api/v1/tenants — ensure the admin belongs to a system workspace
     ("synapse-system"), so builtin model updates (PUT /api/v1/models/{id})
     have a valid tenant context. Skips if the admin already has a workspace.

Required env (from the compose .env; the WEKNORA_ADMIN_* triple MUST match
the Synapse backend's WIKI_AUTH_USERNAME / WIKI_AUTH_EMAIL / WIKI_AUTH_PASSWORD):

  WEKNORA_BASE_URL          e.g. http://app:8080  (no /api/v1 suffix needed)
  WEKNORA_ADMIN_USERNAME
  WEKNORA_ADMIN_EMAIL
  WEKNORA_ADMIN_PASSWORD
  DB_HOST / DB_PORT / DB_USER / DB_PASSWORD / DB_NAME
"""

from __future__ import annotations

import os
import sys
import time

import pg8000.dbapi
import requests

WEKNORA_BASE_URL = os.getenv("WEKNORA_BASE_URL", "http://app:8080").rstrip("/")
ADMIN_USERNAME = os.environ["WEKNORA_ADMIN_USERNAME"]
ADMIN_EMAIL = os.environ["WEKNORA_ADMIN_EMAIL"]
ADMIN_PASSWORD = os.environ["WEKNORA_ADMIN_PASSWORD"]

DB_HOST = os.getenv("DB_HOST", "postgres")
DB_PORT = int(os.getenv("DB_PORT", "5432"))
DB_USER = os.environ["DB_USER"]
DB_PASSWORD = os.environ["DB_PASSWORD"]
DB_NAME = os.environ["DB_NAME"]

HEALTH_RETRIES = 60
HEALTH_INTERVAL = 2


def wait_for_app() -> None:
    """Block until the WeKnora app answers /health."""
    url = f"{WEKNORA_BASE_URL}/health"
    for attempt in range(1, HEALTH_RETRIES + 1):
        try:
            resp = requests.get(url, timeout=3)
            if resp.ok:
                print(f"[init-admin] app healthy after {attempt} attempt(s)")
                return
        except requests.RequestException:
            pass
        time.sleep(HEALTH_INTERVAL)
    sys.exit("[init-admin] app did not become healthy in time")


def register_user() -> None:
    """Create the admin account; tolerate 'already exists'."""
    resp = requests.post(
        f"{WEKNORA_BASE_URL}/api/v1/auth/register",
        json={
            "username": ADMIN_USERNAME,
            "email": ADMIN_EMAIL,
            "password": ADMIN_PASSWORD,
        },
        timeout=10,
    )
    body = resp.json() if resp.content else {}
    if resp.ok and body.get("success"):
        print(f"[init-admin] registered user {ADMIN_USERNAME} <{ADMIN_EMAIL}>")
        return
    text = str(body)
    if "already" in text.lower() or "exists" in text.lower():
        print(f"[init-admin] user {ADMIN_EMAIL} already exists, skipping register")
        return
    sys.exit(f"[init-admin] register failed: HTTP {resp.status_code} {text[:300]}")


def promote_system_admin() -> None:
    """Promote the account to SystemAdmin via direct SQL (idempotent)."""
    conn = pg8000.dbapi.connect(
        host=DB_HOST,
        port=DB_PORT,
        user=DB_USER,
        password=DB_PASSWORD,
        database=DB_NAME,
        timeout=10,
    )
    try:
        cur = conn.cursor()
        cur.execute(
            "UPDATE users SET is_system_admin = true "
            "WHERE email = %s AND is_system_admin = false",
            (ADMIN_EMAIL,),
        )
        updated = cur.rowcount
        cur.execute(
            "SELECT is_system_admin FROM users WHERE email = %s",
            (ADMIN_EMAIL,),
        )
        row = cur.fetchone()
        conn.commit()
    finally:
        conn.close()
    if row is None:
        sys.exit(f"[init-admin] user {ADMIN_EMAIL} not found after register")
    if updated:
        print(f"[init-admin] promoted {ADMIN_EMAIL} to SystemAdmin")
    else:
        print(f"[init-admin] {ADMIN_EMAIL} is already SystemAdmin (no-op)")


SYSTEM_TENANT_NAME = "synapse-system"


def ensure_system_workspace() -> None:
    """
    Ensure the admin belongs to a workspace so builtin model updates have
    tenant context (PUT /api/v1/models/{id} returns 409 without it).

    Idempotent: login → check memberships → skip if admin already has a
    non-default tenant; otherwise create "{SYSTEM_TENANT_NAME}" (API key
    not needed — the admin only uses this workspace for model management).
    """
    # 1. Login
    login_resp = requests.post(
        f"{WEKNORA_BASE_URL}/api/v1/auth/login",
        json={"email": ADMIN_EMAIL, "password": ADMIN_PASSWORD},
        timeout=10,
    )
    login_body = login_resp.json() if login_resp.content else {}
    if not login_resp.ok or not login_body.get("success"):
        sys.exit(
            f"[init-admin] admin login failed: HTTP {login_resp.status_code} "
            f"{str(login_body)[:200]}"
        )

    memberships = login_body.get("memberships") or []
    has_workspace = any(
        str(m.get("tenant_id", "0")) != "0" for m in memberships
    )
    if has_workspace:
        print(
            f"[init-admin] admin already has workspace(s), skip tenant creation"
        )
        return

    # 2. Create system tenant (admin becomes owner automatically)
    token = login_body["token"]
    create_resp = requests.post(
        f"{WEKNORA_BASE_URL}/api/v1/tenants",
        json={
            "name": SYSTEM_TENANT_NAME,
            "description": "Synapse system workspace — builtin model management",
        },
        headers={"Authorization": f"Bearer {token}"},
        timeout=10,
    )
    create_body = create_resp.json() if create_resp.content else {}
    data = create_body.get("data") if isinstance(create_body, dict) else None
    tenant_id = data.get("id") if isinstance(data, dict) else None
    if not create_resp.ok or tenant_id is None:
        sys.exit(
            f"[init-admin] create system tenant failed: HTTP {create_resp.status_code} "
            f"{str(create_body)[:300]}"
        )
    print(
        f"[init-admin] created system workspace "
        f"'{SYSTEM_TENANT_NAME}' (tenant_id={tenant_id})"
    )


def main() -> None:
    wait_for_app()
    register_user()
    promote_system_admin()
    ensure_system_workspace()
    print("[init-admin] done")


if __name__ == "__main__":
    main()
