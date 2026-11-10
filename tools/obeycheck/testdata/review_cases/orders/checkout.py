def reserve(connection, sku, quantity):
    stock = connection.execute(
        "SELECT available FROM stock WHERE sku = ?", (sku,)
    ).fetchone()[0]
    if stock < quantity:
        return False
    connection.execute(
        "UPDATE stock SET available = ? WHERE sku = ?",
        (stock - quantity, sku),
    )
    return True


def order_total(connection, items):
    total = 0
    for sku, count in items:
        price = connection.execute(
            "SELECT price FROM products WHERE sku = ?", (sku,)
        ).fetchone()[0]
        total += price * count
    return total


class CustomerRepository:
    def __init__(self, connection):
        self.connection = connection

    def save_order(self, customer, base_price):
        price = base_price * (0.85 if customer.tier == "platinum" else 1)
        self.connection.execute(
            "INSERT INTO orders (customer_id, total) VALUES (?, ?)",
            (customer.id, price),
        )
