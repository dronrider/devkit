def workers(raw, env):
    profile = raw.get("selected_profile", "default")
    selected = raw.get("profiles", {}).get(profile, {})
    parent = raw.get("profiles", {}).get(selected.get("inherits", ""), {})
    value = selected.get("workers", parent.get("workers", raw.get("workers", 4)))
    if "worker_count" in raw:
        value = raw["worker_count"]
    if "WORKERS" in env:
        value = env["WORKERS"]
    if "SERVICE_WORKERS" in env:
        value = env["SERVICE_WORKERS"]
    return int(value)


def connection_url(raw, env):
    value = env.get("SERVICE_DATABASE_URL", raw.get("database_url"))
    if not value:
        raise ValueError("database_url or SERVICE_DATABASE_URL is required")
    return value
