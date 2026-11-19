#!/usr/bin/python
# Copyright 2024 Google LLC
# Licensed under the Apache License, Version 2.0

from concurrent import futures
import os
import sys
import grpc
import bcrypt
import jwt
import datetime
import uuid

import demo_pb2
import demo_pb2_grpc
from grpc_health.v1 import health_pb2
from grpc_health.v1 import health_pb2_grpc
from user_store import UserStore

from opentelemetry import trace
from opentelemetry.instrumentation.grpc import GrpcInstrumentorServer
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter

from logger import getJSONLogger
logger = getJSONLogger('userservice-server')

PORT = os.environ.get('PORT', '50052')
JWT_SECRET = os.environ.get('JWT_SECRET', 'changeme-use-a-k8s-secret')
JWT_ALGORITHM = 'HS256'
JWT_EXPIRY_HOURS = 24


def _make_token(user_id: str, email: str) -> str:
    payload = {
        'sub': user_id,
        'email': email,
        'iat': datetime.datetime.utcnow(),
        'exp': datetime.datetime.utcnow() + datetime.timedelta(hours=JWT_EXPIRY_HOURS),
    }
    return jwt.encode(payload, JWT_SECRET, algorithm=JWT_ALGORITHM)


class UserServicer(demo_pb2_grpc.UserServiceServicer):
    def __init__(self):
        self.store = UserStore()

    def Check(self, request, context):
        return health_pb2.HealthCheckResponse(status=health_pb2.HealthCheckResponse.SERVING)

    def Watch(self, request, context):
        return health_pb2.HealthCheckResponse(status=health_pb2.HealthCheckResponse.UNIMPLEMENTED)

    def Register(self, request, context):
        logger.info(f"Register called for email={request.email}")
        if not request.email or not request.password or not request.name:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "email, password and name are required")

        existing = self.store.get_user_by_email(request.email)
        if existing:
            context.abort(grpc.StatusCode.ALREADY_EXISTS, "email already registered")

        password_hash = bcrypt.hashpw(request.password.encode('utf-8'), bcrypt.gensalt()).decode('utf-8')
        try:
            user = self.store.create_user(request.email, request.name, password_hash)
        except Exception as e:
            logger.error(f"DB error on Register: {e}")
            context.abort(grpc.StatusCode.INTERNAL, "failed to create user")

        token = _make_token(str(user['id']), user['email'])
        return demo_pb2.RegisterResponse(user_id=str(user['id']), jwt_token=token)

    def Login(self, request, context):
        logger.info(f"Login called for email={request.email}")
        user = self.store.get_user_by_email(request.email)
        if not user:
            context.abort(grpc.StatusCode.NOT_FOUND, "user not found")

        if not bcrypt.checkpw(request.password.encode('utf-8'), user['password_hash'].encode('utf-8')):
            context.abort(grpc.StatusCode.UNAUTHENTICATED, "invalid password")

        token = _make_token(str(user['id']), user['email'])
        return demo_pb2.LoginResponse(user_id=str(user['id']), jwt_token=token, name=user['name'])

    def GetUser(self, request, context):
        logger.info(f"GetUser called for user_id={request.user_id}")
        user = self.store.get_user_by_id(request.user_id)
        if not user:
            context.abort(grpc.StatusCode.NOT_FOUND, "user not found")
        return demo_pb2.GetUserResponse(user_id=str(user['id']), email=user['email'], name=user['name'])


def serve():
    # Set up OTel tracing if configured
    if os.environ.get('ENABLE_TRACING') == '1':
        collector = os.environ.get('COLLECTOR_SERVICE_ADDR', 'otelcollector:4317')
        provider = TracerProvider()
        provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter(endpoint=collector)))
        trace.set_tracer_provider(provider)
        GrpcInstrumentorServer().instrument()
        logger.info("Tracing enabled")

    server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
    servicer = UserServicer()

    # Register user service
    demo_pb2_grpc.add_UserServiceServicer_to_server(servicer, server)

    # Register health service
    health_pb2_grpc.add_HealthServicer_to_server(servicer, server)

    server.add_insecure_port(f'[::]:{PORT}')
    logger.info(f"UserService started on port {PORT}")
    server.start()
    server.wait_for_termination()


if __name__ == '__main__':
    serve()
