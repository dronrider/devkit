def create_order(connection, customer_id, items):
    connection.execute("BEGIN IMMEDIATE")
    try:
        cursor = connection.execute(
            "INSERT INTO orders (customer_id, total) VALUES (?, 0)",
            (customer_id,),
        )
        order_id = cursor.lastrowid
        connection.executemany(
            "INSERT INTO order_items (order_id, sku, quantity) VALUES (?, ?, ?)",
            [(order_id, sku, count) for sku, count in items],
        )
        connection.commit()
        return order_id
    except Exception:
        connection.rollback()
        raise


def save_calculated_total(connection, order_id, total):
    connection.execute(
        "UPDATE orders SET total = ? WHERE id = ?", (total, order_id)
    )


def load_prices(connection, skus):
    unique = list(dict.fromkeys(skus))
    result = {}
    for start in range(0, len(unique), 200):
        batch = unique[start:start + 200]
        placeholders = ",".join("?" for _ in batch)
        result.update(connection.execute(
            "SELECT sku, price FROM products WHERE sku IN (" + placeholders + ")",
            batch,
        ).fetchall())
    return result
