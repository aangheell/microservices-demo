#!/bin/bash
# Regenerate gRPC stubs from the shared proto
python -m grpc_tools.protoc -I../../protos \
    --python_out=. \
    --grpc_python_out=. \
    ../../protos/demo.proto
echo "Proto stubs regenerated"
