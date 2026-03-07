# Build whisper.cpp static libraries
FROM alpine:latest AS whisper-builder

RUN apk add --no-cache git cmake build-base linux-headers

ARG WHISPER_VERSION=1.8.3
RUN git clone --depth 1 -b v${WHISPER_VERSION} https://github.com/ggerganov/whisper.cpp /whisper

WORKDIR /whisper
RUN cmake -B build -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF \
    && cmake --build build -j$(nproc)

# Download whisper model
RUN mkdir -p /whisper-model && \
    wget -O /whisper-model/ggml-tiny.en.bin \
    https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-tiny.en.bin

# Build goclaw
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache gcc musl-dev build-base

# Copy whisper libraries and headers
COPY --from=whisper-builder /whisper/build/src/libwhisper.a /usr/local/lib/
COPY --from=whisper-builder /whisper/build/ggml/src/libggml.a /usr/local/lib/
COPY --from=whisper-builder /whisper/build/ggml/src/libggml-base.a /usr/local/lib/
COPY --from=whisper-builder /whisper/build/ggml/src/libggml-cpu.a /usr/local/lib/
COPY --from=whisper-builder /whisper/include/whisper.h /usr/local/include/
COPY --from=whisper-builder /whisper/ggml/include/*.h /usr/local/include/

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ENV CGO_CFLAGS="-DSQLITE_ENABLE_FTS5 -I/usr/local/include"
ENV CGO_LDFLAGS="-L/usr/local/lib /usr/local/lib/libwhisper.a /usr/local/lib/libggml.a /usr/local/lib/libggml-base.a /usr/local/lib/libggml-cpu.a -lm -lstdc++ -lpthread"
RUN go build -o bin/goclaw ./cmd/goclaw

# Runtime stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates ffmpeg

# Create non-root user
RUN adduser -D -h /home/goclaw goclaw

WORKDIR /home/goclaw

# Copy binary and entrypoint
COPY --from=builder /app/bin/goclaw /usr/local/bin/
COPY --chown=goclaw:goclaw docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Copy whisper model to user's expected location
RUN mkdir -p /home/goclaw/.goclaw/stt/whisper
COPY --chown=goclaw:goclaw --from=whisper-builder /whisper-model/ggml-tiny.en.bin /home/goclaw/.goclaw/stt/whisper/

USER goclaw

EXPOSE 1337

ENTRYPOINT ["/entrypoint.sh"]
