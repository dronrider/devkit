def customer_names(connection):
    rows = connection.execute(
        "SELECT id, name, avatar_blob FROM customers WHERE active = 1"
    ).fetchall()
    return [row[1] for row in rows]


def active_customer_names(connection):
    rows = connection.execute(
        "SELECT name FROM customers WHERE active = 1 ORDER BY id"
    ).fetchall()
    return [row[0] for row in rows]
