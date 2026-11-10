import sqlite3
import unittest


class SchemaIntegrationTest(unittest.TestCase):
    def test_unique_name(self):
        with sqlite3.connect(":memory:") as connection:
            connection.execute("CREATE TABLE policies (name TEXT UNIQUE)")
            connection.execute("INSERT INTO policies VALUES ('daily')")
            with self.assertRaises(sqlite3.IntegrityError):
                connection.execute("INSERT INTO policies VALUES ('daily')")
