"""Плоский отчёт по описанию; публичные данные передаёт репозиторий."""

SOURCES = {
    "profile_name": ("string", lambda field, profile, counters: profile["name"]),
    "counter": ("integer", lambda field, profile, counters: counters[field["key"]]),
}


def validate(fields):
    names = set()
    for index, field in enumerate(fields):
        prefix = f"fields[{index}]"
        if not field.get("name") or field["name"] == "schema_version":
            raise ValueError(f"{prefix}.name must be nonempty and not schema_version")
        if field["name"] in names:
            raise ValueError(f"{prefix}.name is duplicated")
        names.add(field["name"])
        if field.get("source") not in SOURCES:
            raise ValueError(f"{prefix}.source is unsupported")
        expected_type = SOURCES[field["source"]][0]
        if field.get("type") != expected_type:
            raise ValueError(f"{prefix}.type must be {expected_type}")
        if field["source"] == "counter" and not field.get("key"):
            raise ValueError(f"{prefix}.key is required")


def render(fields, repository):
    validate(fields)
    profile = repository.load_public_profile()
    counters = repository.load_counters()
    result = {"schema_version": 1}
    for field in fields:
        result[field["name"]] = SOURCES[field["source"]][1](field, profile, counters)
    return result
