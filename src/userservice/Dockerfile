# Copyright 2024 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

FROM python:3.11-slim@sha256:07d4824036af339fa1b6ef769cc99d9c10a141769538b52b3b597c84f7a7b535 AS base

FROM base AS builder

# Install locked dependencies into a separate prefix so only they are copied
# into the final image.
COPY requirements.txt .
RUN pip install --no-cache-dir --prefix=/install -r requirements.txt

FROM base

ENV PYTHONUNBUFFERED=1

WORKDIR /userservice

COPY --from=builder /install /usr/local

# demo_pb2*.py are generated from protos/demo.proto by genproto.sh.
COPY *.py ./
RUN rm -f test_*.py

# Run as an unprivileged user even outside Kubernetes.
USER 1000:1000

EXPOSE 50052

ENTRYPOINT ["python", "server.py"]
