#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PRISM="${PRISM_ROOT:-/var/home/jhein/.var/app/org.prismlauncher.PrismLauncher/data/PrismLauncher}"
MC="${MC_DIR:-$PRISM/instances/GT_New_Horizons_2.8.3_Java_17-25/.minecraft}"
LIB="$PRISM/libraries"

DH_JAR="${DH_JAR:-$MC/mods/distanthorizons-alpha18-codex-gtnh-2-8-3-compat.jar}"
SQLITE_JAR="${SQLITE_JAR:-$MC/mods/sqlite-jdbc-3.53.0.0.jar}"

CP="$DH_JAR:$SQLITE_JAR"
CP="$CP:$LIB/it/unimi/dsi/fastutil/8.5.18/fastutil-8.5.18.jar"
CP="$CP:$LIB/io/netty/netty-all/4.0.10.Final/netty-all-4.0.10.Final.jar"
CP="$CP:$LIB/com/google/guava/guava/17.0/guava-17.0.jar"
CP="$CP:$LIB/org/apache/logging/log4j/log4j-api/2.0-beta9-fixed/log4j-api-2.0-beta9-fixed.jar"
CP="$CP:$LIB/org/apache/logging/log4j/log4j-core/2.0-beta9-fixed/log4j-core-2.0-beta9-fixed.jar"
CP="$CP:$LIB/at/yawk/lz4/lz4-java/1.10.1/lz4-java-1.10.1.jar"

mkdir -p "$ROOT/build/classes"
javac --release 17 -cp "$CP" -d "$ROOT/build/classes" "$ROOT/src/main/java/DhCacheChunkConverter.java"

cat > "$ROOT/build/classpath.txt" <<EOF
$ROOT/build/classes:$CP
EOF

echo "Built converter classes in $ROOT/build/classes"
