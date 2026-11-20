#!/usr/bin/env bash
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

# Creates the "userservice-secret" Secret with random credentials, unless it
# already exists. Safe to run on every deploy.
#
# The existing Secret is never replaced: Postgres only reads POSTGRES_PASSWORD
# when it first initialises its volume, so a new db-password would lock
# userservice out of the existing database.
#
# Usage: create-secret.sh [namespace]   (defaults to the current context's)

set -euo pipefail

SECRET=userservice-secret
NS_ARGS=()
if [[ $# -ge 1 && -n "$1" ]]; then
  NS_ARGS=(--namespace "$1")
fi

# Credentials that were committed to git in earlier versions of this repo.
OLD_DB_PASSWORD=shoppassword
OLD_JWT_SECRET=online-boutique-jwt-secret-key-change-in-prod

random() {
  head -c 64 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 48
}

if kubectl get secret "$SECRET" "${NS_ARGS[@]}" >/dev/null 2>&1; then
  echo "Secret $SECRET already exists; leaving it unchanged."
  db=$(kubectl get secret "$SECRET" "${NS_ARGS[@]}" -o jsonpath='{.data.db-password}' | base64 -d)
  jwt=$(kubectl get secret "$SECRET" "${NS_ARGS[@]}" -o jsonpath='{.data.jwt-secret}' | base64 -d)
  if [[ "$db" == "$OLD_DB_PASSWORD" || "$jwt" == "$OLD_JWT_SECRET" ]]; then
    echo "WARNING: $SECRET still holds credentials that were published in git." >&2
    echo "         See kustomize/components/user-service/README.md to rotate them." >&2
  fi
  exit 0
fi

# Values go through a process substitution so they never appear in argv.
kubectl create secret generic "$SECRET" "${NS_ARGS[@]}" \
  --from-env-file=<(printf 'db-password=%s\njwt-secret=%s\n' "$(random)" "$(random)")
echo "Created secret $SECRET."
