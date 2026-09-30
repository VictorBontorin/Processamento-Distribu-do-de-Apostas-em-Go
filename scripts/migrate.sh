#!/bin/sh
# Aplica (up) ou reverte (down [N]) as migrations versionadas.
# Mantém a tabela schema_migrations com as versões aplicadas.
#
#   sh scripts/migrate.sh up
#   sh scripts/migrate.sh down       # reverte a última migration
#   sh scripts/migrate.sh down 3     # reverte as 3 últimas
#
# Variáveis: DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME, MIGRATIONS_DIR.
set -eu

CMD="${1:-up}"
DIR="${MIGRATIONS_DIR:-$(dirname "$0")/../migrations}"

export PGPASSWORD="${DB_PASSWORD:-wager}"

psql_run() {
  psql -v ON_ERROR_STOP=1 -q \
    -h "${DB_HOST:-localhost}" -p "${DB_PORT:-5432}" \
    -U "${DB_USER:-wager}" -d "${DB_NAME:-wager}" "$@"
}

psql_run -c "CREATE TABLE IF NOT EXISTS schema_migrations (
  version TEXT PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)"

case "$CMD" in
  up)
    for file in "$DIR"/*.sql; do
      version=$(basename "$file" .sql)
      applied=$(psql_run -tA -c "SELECT 1 FROM schema_migrations WHERE version = '$version'")

      if [ -z "$applied" ]; then
        echo "applying $version"
        psql_run --single-transaction -f "$file"
        psql_run -c "INSERT INTO schema_migrations (version) VALUES ('$version')"
      fi
    done
    echo "migrations up to date"
    ;;

  down)
    steps="${2:-1}"
    i=0

    while [ "$i" -lt "$steps" ]; do
      version=$(psql_run -tA -c "SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1")

      if [ -z "$version" ]; then
        echo "nothing to roll back"
        exit 0
      fi

      down="$DIR/down/$version.down.sql"

      if [ ! -f "$down" ]; then
        echo "no down migration for $version" >&2
        exit 1
      fi

      echo "reverting $version"
      psql_run --single-transaction -f "$down"
      psql_run -c "DELETE FROM schema_migrations WHERE version = '$version'"
      i=$((i + 1))
    done
    ;;

  *)
    echo "usage: $0 up | down [N]" >&2
    exit 2
    ;;
esac
