#!/command/with-contenv /opt/hermes/.venv/bin/python
"""Seed declarative model settings and the bundled meal MCP in Hermes."""

from __future__ import annotations

import os
import shlex
from pathlib import Path

import yaml


MCP_NAME = "meal-recommendation"
MCP_CONFIG = {
    "command": "/usr/local/bin/start-meal-mcp",
    "enabled": True,
    "connect_timeout": 20,
    "timeout": 60,
    "supports_parallel_tool_calls": False,
    "tools": {
        "include": [
            "start_recommendation_session",
            "get_recommendation_session",
            "update_meal_requirement",
            "generate_meal_recommendations",
            "list_product_catalog",
            "search_product_catalog",
        ],
        "prompts": False,
        "resources": False,
    },
}

MCP_ENVIRONMENT_KEYS = (
    "APP_SESSION_TTL",
    "APP_DATABASE_URL",
    "APP_CATALOG_SEED_PATH",
    "APP_CATALOG_SEED_MODE",
    "APP_STARTUP_TIMEOUT",
    "APP_MCD_MCP_ENDPOINT",
    "APP_MCD_MCP_TOKEN",
    "APP_EXTERNAL_TIMEOUT",
    "APP_CACHE_TTL",
    "APP_CACHE_STALE_TTL",
    "APP_OFFER_STALE_AFTER",
    "APP_MCD_MAX_DETAILS",
)


def atomic_write(path: Path, content: str, mode: int) -> None:
    temporary = path.with_name(path.name + ".tmp")
    temporary.write_text(content, encoding="utf-8")
    temporary.chmod(mode)
    temporary.replace(path)


def required_environment(key: str) -> str:
    value = os.environ.get(key, "").strip()
    if not value:
        raise RuntimeError(f"{key} must not be empty")
    return value


def seed_config(home: Path) -> None:
    path = home / "config.yaml"
    if path.exists():
        loaded = yaml.safe_load(path.read_text(encoding="utf-8"))
        config = loaded if isinstance(loaded, dict) else {}
    else:
        config = {}

    changed = False

    provider = required_environment("HERMES_MODEL_PROVIDER")
    model_name = required_environment("HERMES_MODEL_NAME")
    model = config.setdefault("model", {})
    if not isinstance(model, dict):
        raise RuntimeError("config.yaml model must be a mapping")
    if model.get("provider") != provider:
        model["provider"] = provider
        changed = True
    if model.get("default") != model_name:
        model["default"] = model_name
        changed = True

    servers = config.setdefault("mcp_servers", {})
    if not isinstance(servers, dict):
        raise RuntimeError("config.yaml mcp_servers must be a mapping")

    # Once present, the MCP entry belongs to the operator. This keeps Dashboard
    # or CLI edits to tool filters and timeouts stable across image restarts.
    if MCP_NAME not in servers:
        servers[MCP_NAME] = MCP_CONFIG
        changed = True

    if changed:
        atomic_write(path, yaml.safe_dump(config, sort_keys=False, allow_unicode=True), 0o600)
        print(f"[meal-mcp] synchronized Hermes model and MCP configuration in {path}")


def seed_environment(home: Path) -> None:
    lines = []
    for key in MCP_ENVIRONMENT_KEYS:
        value = os.environ.get(key)
        if value is not None:
            lines.append(f"{key}={shlex.quote(value)}")
    atomic_write(home / "meal-mcp.env", "\n".join(lines) + "\n", 0o600)


def main() -> None:
    home = Path(os.environ.get("HERMES_HOME", "/opt/data"))
    home.mkdir(parents=True, exist_ok=True)
    seed_config(home)
    seed_environment(home)
    # Keep workflow instructions aligned with the bundled server contract.
    skill_source = Path("/usr/local/share/meal-recommendation-skill")
    skill_target = home / "skills/productivity/meal-recommendation"
    for source in skill_source.rglob("*.md"):
        target = skill_target / source.relative_to(skill_source)
        target.parent.mkdir(parents=True, exist_ok=True)
        atomic_write(target, source.read_text(encoding="utf-8"), 0o644)

    # The hook runs as root before s6 launches services. Resolve the runtime
    # account by name so deployments using HERMES_UID remapping still work.
    import grp
    import pwd

    user = pwd.getpwnam("hermes")
    group = grp.getgrnam("hermes")
    for path in skill_target.rglob("*"):
        os.chown(path, user.pw_uid, group.gr_gid)
    if skill_target.exists():
        os.chown(skill_target, user.pw_uid, group.gr_gid)
    for name in ("config.yaml", "meal-mcp.env"):
        path = home / name
        if path.exists():
            os.chown(path, user.pw_uid, group.gr_gid)


if __name__ == "__main__":
    main()
