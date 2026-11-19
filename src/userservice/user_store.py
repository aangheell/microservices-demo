import os
import uuid
import psycopg2
import psycopg2.extras

from logger import getJSONLogger
logger = getJSONLogger('userservice-store')

CREATE_TABLE_SQL = """
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) UNIQUE NOT NULL,
    name VARCHAR(255) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);
"""

class UserStore:
    def __init__(self):
        db_url = os.environ.get('DB_URL', 'postgresql://user:password@postgres-user:5432/usersdb')
        self.conn = psycopg2.connect(db_url)
        self.conn.autocommit = True
        self._init_db()
        logger.info("UserStore initialized")

    def _init_db(self):
        with self.conn.cursor() as cur:
            cur.execute(CREATE_TABLE_SQL)
        logger.info("Database schema initialized")

    def create_user(self, email, name, password_hash):
        with self.conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor) as cur:
            cur.execute(
                "INSERT INTO users (email, name, password_hash) VALUES (%s, %s, %s) RETURNING id, email, name",
                (email, name, password_hash)
            )
            return dict(cur.fetchone())

    def get_user_by_email(self, email):
        with self.conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor) as cur:
            cur.execute("SELECT id, email, name, password_hash FROM users WHERE email = %s", (email,))
            row = cur.fetchone()
            return dict(row) if row else None

    def get_user_by_id(self, user_id):
        with self.conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor) as cur:
            cur.execute("SELECT id, email, name FROM users WHERE id = %s", (user_id,))
            row = cur.fetchone()
            return dict(row) if row else None
