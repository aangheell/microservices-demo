#!/usr/bin/python
# Copyright 2024 Google LLC
# Licensed under the Apache License, Version 2.0

from concurrent import futures
import collections
import datetime
import hashlib
import os
import re
import sys
import threading
import time
import uuid

import bcrypt
import grpc
import jwt

import demo_pb2
import demo_pb2_grpc
from grpc_health.v1 import health_pb2
from grpc_health.v1 import health_pb2_grpc
from user_store import UserAlreadyExists

from logger import getJSONLogger
logger = getJSONLogger('userservice-server')

SERVICE_NAME = 'hipstershop.UserService'
PORT = os.environ.get('PORT', '50052')
MAX_WORKERS = 10

# These claims are checked by the frontend when it verifies the token.
JWT_ALGORITHM = 'HS256'
JWT_ISSUER = 'userservice'
JWT_AUDIENCE = 'frontend'
JWT_EXPIRY = datetime.timedelta(hours=24)
JWT_SECRET_MIN_BYTES = 32

PASSWORD_MIN_LEN = 8
PASSWORD_MAX_BYTES = 72  # bcrypt ignores everything past 72 bytes
NAME_MAX_LEN = 255
EMAIL_MAX_LEN = 255
EMAIL_RE = re.compile(r'^[^@\s]+@[^@\s]+\.[^@\s]+$')

LOGIN_MAX_FAILURES = 5
LOGIN_FAILURE_WINDOW_S = 15 * 60

INVALID_CREDENTIALS = "invalid email or password"


def load_jwt_secret():
    secret = os.environ.get('JWT_SECRET', '')
    if len(secret.encode('utf-8')) < JWT_SECRET_MIN_BYTES:
        sys.exit(f"JWT_SECRET must be set to at least {JWT_SECRET_MIN_BYTES} bytes")
    return secret


def email_ref(email):
    """Short, non-reversible reference to an email so logs carry no PII."""
    return hashlib.sha256(email.encode('utf-8')).hexdigest()[:12]


def normalize_email(email):
    return email.strip().lower()


class LoginThrottle:
    """Locks an email out after repeated failed logins within a time window.

    State is per replica, so the effective limit scales with the replica count.
    """

    def __init__(self, max_failures=LOGIN_MAX_FAILURES, window_s=LOGIN_FAILURE_WINDOW_S,
                 clock=time.monotonic):
        self._max = max_failures
        self._window = window_s
        self._clock = clock
        self._failures = collections.defaultdict(collections.deque)
        self._lock = threading.Lock()

    def _prune(self, key, now):
        q = self._failures.get(key)
        while q and now - q[0] > self._window:
            q.popleft()
        if q is not None and not q:
            del self._failures[key]

    def is_locked(self, key):
        with self._lock:
            now = self._clock()
            self._prune(key, now)
            return len(self._failures.get(key, ())) >= self._max

    def record_failure(self, key):
        with self._lock:
            now = self._clock()
            self._prune(key, now)
            self._failures[key].append(now)

    def reset(self, key):
        with self._lock:
            self._failures.pop(key, None)


class UserServicer(demo_pb2_grpc.UserServiceServicer):
    def __init__(self, store, jwt_secret, throttle=None):
        self.store = store
        self._jwt_secret = jwt_secret
        self._throttle = throttle or LoginThrottle()
        # Compared against when the email is unknown, so a login takes the same
        # time whether or not the account exists.
        self._dummy_hash = bcrypt.hashpw(os.urandom(16), bcrypt.gensalt())

    def _make_token(self, user_id, email):
        now = datetime.datetime.now(datetime.timezone.utc)
        payload = {
            'sub': user_id,
            'email': email,
            'iss': JWT_ISSUER,
            'aud': JWT_AUDIENCE,
            'iat': now,
            'exp': now + JWT_EXPIRY,
        }
        return jwt.encode(payload, self._jwt_secret, algorithm=JWT_ALGORITHM)

    def Register(self, request, context):
        email = normalize_email(request.email)
        name = request.name.strip()
        password = request.password.encode('utf-8')

        if not email or not password or not name:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "email, password and name are required")
        if len(email) > EMAIL_MAX_LEN or not EMAIL_RE.match(email):
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "invalid email address")
        if len(name) > NAME_MAX_LEN:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "name is too long")
        if len(request.password) < PASSWORD_MIN_LEN:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT,
                          f"password must be at least {PASSWORD_MIN_LEN} characters")
        if len(password) > PASSWORD_MAX_BYTES:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT,
                          f"password must be at most {PASSWORD_MAX_BYTES} bytes")

        password_hash = bcrypt.hashpw(password, bcrypt.gensalt()).decode('utf-8')
        try:
            user = self.store.create_user(email, name, password_hash)
        except UserAlreadyExists:
            logger.info(f"Register rejected, email exists (ref={email_ref(email)})")
            context.abort(grpc.StatusCode.ALREADY_EXISTS, "email already registered")
        except Exception as e:
            logger.error(f"DB error on Register: {type(e).__name__}")
            context.abort(grpc.StatusCode.INTERNAL, "failed to create user")

        user_id = str(user['id'])
        logger.info(f"Registered user_id={user_id}")
        return demo_pb2.RegisterResponse(user_id=user_id, jwt_token=self._make_token(user_id, user['email']))

    def Login(self, request, context):
        email = normalize_email(request.email)
        password = request.password.encode('utf-8')
        ref = email_ref(email)

        if self._throttle.is_locked(email):
            logger.warning(f"Login throttled (ref={ref})")
            context.abort(grpc.StatusCode.RESOURCE_EXHAUSTED, "too many failed attempts, try again later")

        try:
            user = self.store.get_user_by_email(email)
        except Exception as e:
            logger.error(f"DB error on Login: {type(e).__name__}")
            context.abort(grpc.StatusCode.UNAVAILABLE, "user store unavailable")

        stored_hash = user['password_hash'].encode('utf-8') if user else self._dummy_hash
        password_ok = bcrypt.checkpw(password, stored_hash)

        if not user or not password_ok:
            self._throttle.record_failure(email)
            logger.info(f"Login failed (ref={ref})")
            context.abort(grpc.StatusCode.UNAUTHENTICATED, INVALID_CREDENTIALS)

        self._throttle.reset(email)
        user_id = str(user['id'])
        logger.info(f"Login succeeded user_id={user_id}")
        return demo_pb2.LoginResponse(
            user_id=user_id, jwt_token=self._make_token(user_id, user['email']), name=user['name'])

    def GetUser(self, request, context):
        try:
            user_id = str(uuid.UUID(request.user_id))
        except ValueError:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "invalid user_id")
        user = self.store.get_user_by_id(user_id)
        if not user:
            context.abort(grpc.StatusCode.NOT_FOUND, "user not found")
        return demo_pb2.GetUserResponse(user_id=str(user['id']), email=user['email'], name=user['name'])


class HealthServicer(health_pb2_grpc.HealthServicer):
    """gRPC health checks.

    - service "" (liveness): the process is up and serving RPCs.
    - service "hipstershop.UserService" (readiness): the database is reachable too.
    Keeping the DB out of liveness stops a DB outage from restart-looping the pod.
    """

    def __init__(self, store):
        self.store = store

    def Check(self, request, context):
        Status = health_pb2.HealthCheckResponse
        if request.service == '':
            return Status(status=Status.SERVING)
        if request.service == SERVICE_NAME:
            return Status(status=Status.SERVING if self.store.ping() else Status.NOT_SERVING)
        context.abort(grpc.StatusCode.NOT_FOUND, "unknown service")

    def Watch(self, request, context):
        context.abort(grpc.StatusCode.UNIMPLEMENTED, "watch is not supported")


def init_tracing():
    from opentelemetry import trace
    from opentelemetry.instrumentation.grpc import GrpcInstrumentorServer
    from opentelemetry.sdk.trace import TracerProvider
    from opentelemetry.sdk.trace.export import BatchSpanProcessor
    from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter

    collector = os.environ.get('COLLECTOR_SERVICE_ADDR', 'otelcollector:4317')
    provider = TracerProvider()
    provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter(endpoint=collector, insecure=True)))
    trace.set_tracer_provider(provider)
    GrpcInstrumentorServer().instrument()
    logger.info("Tracing enabled")


def serve():
    from user_store import UserStore

    jwt_secret = load_jwt_secret()

    if os.environ.get('ENABLE_TRACING') == '1':
        init_tracing()

    # Connection settings come from DB_HOST, DB_PORT, DB_NAME, DB_USER and
    # DB_PASSWORD; keep DB_POOL_MAX >= MAX_WORKERS so threads never starve.
    store = UserStore()

    server = grpc.server(futures.ThreadPoolExecutor(max_workers=MAX_WORKERS))
    demo_pb2_grpc.add_UserServiceServicer_to_server(UserServicer(store, jwt_secret), server)
    health_pb2_grpc.add_HealthServicer_to_server(HealthServicer(store), server)

    server.add_insecure_port(f'[::]:{PORT}')
    server.start()
    logger.info(f"UserService started on port {PORT}")
    server.wait_for_termination()


if __name__ == '__main__':
    serve()
