#!/bin/sh
set -e

# ==============================================================================
# Xiaoya Media Server All-in-One Entrypoint
# ==============================================================================

INTERNAL_PG=false
PGDATA="${DATA_DIR:-/data}/postgres"
SERVER_PID=""

shutdown_handler() {
    echo ""
    echo "[Entrypoint] Received shutdown signal. Gracefully stopping services..."
    if [ -n "$SERVER_PID" ]; then
        echo "[Entrypoint] Stopping Xiaoya Media Server (PID: $SERVER_PID)..."
        kill -TERM "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi

    if [ "$INTERNAL_PG" = "true" ]; then
        echo "[Entrypoint] Stopping internal PostgreSQL..."
        su-exec postgres pg_ctl -D "$PGDATA" -m fast -w stop || true
    fi

    echo "[Entrypoint] All services stopped safely."
    exit 0
}

trap shutdown_handler SIGTERM SIGINT

PG_HOST="${POSTGRES_HOST:-127.0.0.1}"

if [ "$PG_HOST" = "127.0.0.1" ] || [ "$PG_HOST" = "localhost" ] || [ -z "$PG_HOST" ]; then
    INTERNAL_PG=true
    echo "[Entrypoint] Internal PostgreSQL mode enabled."

    mkdir -p /run/postgresql
    chown -R postgres:postgres /run/postgresql
    chmod 775 /run/postgresql

    mkdir -p "$PGDATA"
    chown -R postgres:postgres "$PGDATA"
    chmod 700 "$PGDATA"

    # 若未初始化数据库集群，则执行 initdb
    if [ ! -s "$PGDATA/PG_VERSION" ]; then
        echo "[Entrypoint] Initializing PostgreSQL cluster in $PGDATA..."
        su-exec postgres initdb -D "$PGDATA" -E UTF8 --locale=C -U "${POSTGRES_USER:-emby}" > /dev/null

        # 确保信任本地连接并支持 md5
        cat >> "$PGDATA/pg_hba.conf" <<EOF
host all all 127.0.0.1/32 md5
local all all trust
EOF
    fi

    # 清理非正常关机可能遗留的 postmaster.pid（若对应进程不存在）
    if [ -f "$PGDATA/postmaster.pid" ]; then
        PID=$(head -n 1 "$PGDATA/postmaster.pid" 2>/dev/null || true)
        if [ -n "$PID" ] && ! kill -0 "$PID" 2>/dev/null; then
            echo "[Entrypoint] Removing stale postmaster.pid (PID: $PID)..."
            rm -f "$PGDATA/postmaster.pid"
        fi
    fi

    # 启动内部 PostgreSQL
    echo "[Entrypoint] Starting internal PostgreSQL service..."
    su-exec postgres pg_ctl -D "$PGDATA" -o "-c listen_addresses='127.0.0.1' -c unix_socket_directories='/run/postgresql'" -w start

    # 等待 PostgreSQL 真正就绪
    echo "[Entrypoint] Waiting for internal PostgreSQL to accept connections..."
    for i in $(seq 1 30); do
        if su-exec postgres pg_isready -h 127.0.0.1 -p 5432 -U "${POSTGRES_USER:-emby}" > /dev/null 2>&1; then
            break
        fi
        sleep 1
    done

    DB_USER="${POSTGRES_USER:-emby}"
    DB_PASS="${POSTGRES_PASSWORD:-emby}"
    DB_NAME="${POSTGRES_DB:-emby}"

    echo "[Entrypoint] Ensuring PostgreSQL user '$DB_USER' and database '$DB_NAME' exist..."
    # 确保用户密码正确
    su-exec postgres psql -h 127.0.0.1 -U "$DB_USER" -d postgres -c "ALTER USER \"$DB_USER\" WITH ENCRYPTED PASSWORD '$DB_PASS';" > /dev/null 2>&1 || true

    # 检查目标数据库是否存在，不存在则创建
    if [ "$DB_NAME" != "postgres" ]; then
        DB_EXISTS=$(su-exec postgres psql -h 127.0.0.1 -U "$DB_USER" -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname = '$DB_NAME';" 2>/dev/null || true)
        if [ "$DB_EXISTS" != "1" ]; then
            echo "[Entrypoint] Database '$DB_NAME' not found. Creating..."
            su-exec postgres psql -h 127.0.0.1 -U "$DB_USER" -d postgres -c "CREATE DATABASE \"$DB_NAME\" OWNER \"$DB_USER\";" > /dev/null
            echo "[Entrypoint] Database '$DB_NAME' created successfully."
        else
            echo "[Entrypoint] Database '$DB_NAME' already exists."
        fi
    fi

    export POSTGRES_HOST=127.0.0.1
    export POSTGRES_PORT=5432
    echo "[Entrypoint] Internal PostgreSQL is ready at 127.0.0.1:5432."
else
    echo "[Entrypoint] External PostgreSQL host configured: $PG_HOST:$POSTGRES_PORT"
fi

# 启动 Xiaoya Media Server 主程序
echo "[Entrypoint] Starting Xiaoya Media Server with command: $@"
"$@" &
SERVER_PID=$!

wait "$SERVER_PID"
EXIT_STATUS=$?

if [ "$INTERNAL_PG" = "true" ]; then
    echo "[Entrypoint] Stopping internal PostgreSQL..."
    su-exec postgres pg_ctl -D "$PGDATA" -m fast -w stop || true
fi

exit $EXIT_STATUS
