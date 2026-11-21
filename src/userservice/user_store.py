import os
import time
import uuid

import psycopg2
import psycopg2.extras
import psycopg2.pool
from psycopg2 import errors

from logger import getJSONLogger

logger = getJSONLogger('userservice-store')

SCHEMA_LOCK_ID = 727274  # arbitrary constant for pg_advisory_lock

CREATE_TABLE_SQL = """
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY,
    email VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS users_email_key ON users (email);

-- Order history. "order_data" is the OrderResult as JSON (items, shipping
-- address and cost, tracking ID), so the schema doesn't change when the
-- proto gains fields.
CREATE TABLE IF NOT EXISTS orders (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    total_currency VARCHAR(3) NOT NULL,
    total_units BIGINT NOT NULL,
    total_nanos INTEGER NOT NULL,
    order_data JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS orders_user_created_idx ON orders (user_id, created_at DESC);
"""

# Most orders a user's history returns.
ORDERS_LIST_LIMIT = 50


class UserAlreadyExists(Exception):
    """Raised when registering an email that is already taken."""


class UserNotFound(Exception):
    """Raised when recording an order for a user that doesn't exist."""


def normalize_email(email):
    return (email or "").strip().lower()


def _db_config():
    """Build connection kwargs from env vars. No hard-coded credentials:
    a missing password fails loudly instead of falling back to a default."""
    password = os.environ.get('DB_PASSWORD')
    if not password:
        raise RuntimeError("DB_PASSWORD is not set")
    return dict(
        host=os.environ.get('DB_HOST', 'postgres-user'),
        port=int(os.environ.get('DB_PORT', '5432')),
        dbname=os.environ.get('DB_NAME', 'usersdb'),
        user=os.environ.get('DB_USER', 'user'),
        password=password,
        connect_timeout=int(os.environ.get('DB_CONNECT_TIMEOUT', '5')),
        sslmode=os.environ.get('DB_SSLMODE', 'prefer'),
    )


class UserStore:
    def __init__(self):
        cfg = _db_config()
        max_conn = int(os.environ.get('DB_POOL_MAX', '10'))
        self.pool = self._connect_with_retry(cfg, max_conn)
        self._init_db()
        logger.info("UserStore initialized")

    @staticmethod
    def _connect_with_retry(cfg, max_conn, attempts=10, base_delay=1.0):
        for attempt in range(1, attempts + 1):
            try:
                return psycopg2.pool.ThreadedConnectionPool(1, max_conn, **cfg)
            except psycopg2.OperationalError as e:
                if attempt == attempts:
                    logger.error("Could not connect to database, giving up")
                    raise
                delay = min(base_delay * 2 ** (attempt - 1), 15)
                logger.warning(
                    f"Database not ready (attempt {attempt}/{attempts}), "
                    f"retrying in {delay:.0f}s: {type(e).__name__}")
                time.sleep(delay)

    def _with_conn(self, fn, retry=True):
        """Run fn(conn) on a pooled connection. If the connection turns out to
        be dead (DB restart, network blip), discard it and retry once.
        Pass retry=False for writes: the first attempt may have committed
        before the connection dropped, and repeating it is not safe."""
        for attempt in ((1, 2) if retry else (2,)):
            conn = self.pool.getconn()
            try:
                conn.autocommit = True
                result = fn(conn)
                self.pool.putconn(conn)
                return result
            except (psycopg2.OperationalError, psycopg2.InterfaceError):
                self.pool.putconn(conn, close=True)
                if attempt == 2:
                    raise
                logger.warning("Stale DB connection discarded, retrying once")
            except Exception:
                self.pool.putconn(conn)
                raise

    def _init_db(self):
        def run(conn):
            with conn.cursor() as cur:
                # Serialize schema creation when several replicas start together
                cur.execute("SELECT pg_advisory_lock(%s)", (SCHEMA_LOCK_ID,))
                try:
                    cur.execute(CREATE_TABLE_SQL)
                finally:
                    cur.execute("SELECT pg_advisory_unlock(%s)", (SCHEMA_LOCK_ID,))
        self._with_conn(run)
        logger.info("Database schema initialized")

    def ping(self):
        """Cheap liveness check for a gRPC health endpoint."""
        def run(conn):
            with conn.cursor() as cur:
                cur.execute("SELECT 1")
                return cur.fetchone()[0] == 1
        try:
            return self._with_conn(run)
        except Exception:
            return False

    def create_user(self, email, name, password_hash):
        email = normalize_email(email)
        user_id = uuid.uuid4()

        def run(conn):
            with conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor) as cur:
                cur.execute(
                    "INSERT INTO users (id, email, name, password_hash) "
                    "VALUES (%s, %s, %s, %s) RETURNING id, email, name",
                    (str(user_id), email, name.strip(), password_hash),
                )
                row = dict(cur.fetchone())
                row['id'] = str(row['id'])
                return row
        try:
            return self._with_conn(run, retry=False)
        except errors.UniqueViolation:
            raise UserAlreadyExists(email)

    def get_user_by_email(self, email):
        email = normalize_email(email)

        def run(conn):
            with conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor) as cur:
                cur.execute(
                    # LOWER() also matches rows stored before emails were normalized.
                    "SELECT id, email, name, password_hash FROM users WHERE LOWER(email) = %s",
                    (email,),
                )
                row = cur.fetchone()
                if not row:
                    return None
                row = dict(row)
                row['id'] = str(row['id'])
                return row
        return self._with_conn(run)

    def get_user_by_id(self, user_id):
        # A malformed UUID would make Postgres raise DataError; treat it as "not found"
        try:
            user_id = str(uuid.UUID(str(user_id)))
        except ValueError:
            return None

        def run(conn):
            with conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor) as cur:
                cur.execute("SELECT id, email, name FROM users WHERE id = %s", (user_id,))
                row = cur.fetchone()
                if not row:
                    return None
                row = dict(row)
                row['id'] = str(row['id'])
                return row
        return self._with_conn(run)

    def record_order(self, order_id, user_id, total, order_data):
        """Store an order. total is (currency, units, nanos); order_data is a
        JSON-serializable dict. Recording the same order_id twice is a no-op,
        so callers can retry safely. Returns True if a row was inserted."""
        currency, units, nanos = total

        def run(conn):
            with conn.cursor() as cur:
                cur.execute(
                    "INSERT INTO orders (id, user_id, total_currency, total_units, total_nanos, order_data) "
                    "VALUES (%s, %s, %s, %s, %s, %s) ON CONFLICT (id) DO NOTHING",
                    (str(order_id), str(user_id), currency, units, nanos,
                     psycopg2.extras.Json(order_data)),
                )
                return cur.rowcount == 1
        try:
            return self._with_conn(run)
        except errors.ForeignKeyViolation:
            raise UserNotFound(user_id)

    def list_orders(self, user_id, limit=ORDERS_LIST_LIMIT):
        """The user's most recent orders, newest first."""
        def run(conn):
            with conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor) as cur:
                cur.execute(
                    "SELECT id, total_currency, total_units, total_nanos, order_data, created_at "
                    "FROM orders WHERE user_id = %s ORDER BY created_at DESC LIMIT %s",
                    (str(user_id), limit),
                )
                return [dict(r) for r in cur.fetchall()]
        return self._with_conn(run)

    def close(self):
        self.pool.closeall()