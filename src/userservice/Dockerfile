FROM python:3.11-slim AS base

WORKDIR /userservice

# Install dependencies
COPY requirements.in requirements.in
RUN pip install --no-cache-dir -r requirements.in

# Copy proto stubs from emailservice build context (handled at build time)
COPY demo_pb2.py .
COPY demo_pb2_grpc.py .

COPY *.py .

EXPOSE 50052

ENTRYPOINT ["python", "server.py"]
