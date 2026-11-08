#!/bin/sh
set -e

# ==============================================================================
# Xiaoya Media Server All-in-One Entrypoint
# ==============================================================================

INTERNAL_PG=false
PGDATA="${DATA_DIR:-/data}/postgres"
SERVER_PID=""

# 优雅退出处理
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

# 检测是否使用内置 PostgreSQL（当 POSTGRES_HOST 未指定或为 127.0.0.1 / localhost 时）
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

    # 若未初始化数据库集群，则自动初始化
    if [ ! -s "$PGDATA/PG_VERSION" ]; then
        echo "[Entrypoint] Initializing PostgreSQL cluster in $PGDATA..."
        su-exec postgres initdb -D "$PGDATA" -E UTF8 --locale=C -U "${POSTGRES_USER:-emby}" > /dev/null

        # 配置白名单
        cat >> "$PGDATA/pg_hba.conf" <<EOF
host all all 127.0.0.1/32 md5
local all all trust
EOF

        # 临时启动以设置密码及创建业务库
        su-exec postgres pg_ctl -D "$PGDATA" -o "-c listen_addresses='127.0.0.1' -c unix_socket_directories='/run/postgresql'" -w start > /dev/null

        DB_USER="${POSTGRES_USER:-emby}"
        DB_PASS="${POSTGRES_PASSWORD:-emby}"
        DB_NAME="${POSTGRES_DB:-emby}"

        echo "[Entrypoint] Configuring PostgreSQL user and database..."
        su-exec postgres psql -h 127.0.0.1 -U "$DB_USER" -d postgres -c "ALTER USER \"$DB_USER\" WITH ENCRYPTED PASSWORD '$DB_PASS';" > /dev/null
        if [ "$DB_NAME" != "postgres" ] && [ "$DB_NAME" != "$DB_USER" ]; then
            su-exec postgres psql -h 127.0.0.1 -U "$DB_USER" -d postgres -c "CREATE DATABASE \"$DB_NAME\" OWNER \"$DB_USER\";" 2>/dev/null || true
        fi

        su-exec postgres pg_ctl -D "$PGDATA" -m fast -w stop > /dev/null
        echo "[Entrypoint] PostgreSQL cluster initialization completed."
    fi

    # 启动持久运行的内部 PostgreSQL
    echo "[Entrypoint] Starting internal PostgreSQL service..."
    su-exec postgres pg_ctl -D "$PGDATA" -o "-c listen_addresses='127.0.0.1' -c unix_socket_directories='/run/postgresql'" -w start

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

# 等待主进程结束并透传退出码
wait "$SERVER_PID"
EXIT_STATUS=$?

if [ "$INTERNAL_PG" = "true" ]; then
    echo "[Entrypoint] Stopping internal PostgreSQL..."
    su-exec postgres pg_ctl -D "$PGDATA" -m fast -w stop || true
fi

exit $EXIT_STATUS
