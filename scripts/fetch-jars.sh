#!/usr/bin/env bash
# 下载 owljdbc 测试与运行所需的 JDBC 驱动 jar（Maven Central）。
# 版本可按需增删。
set -eu
cd "$(dirname "$0")/.."   # 仓库根（jars 落在当前目录）

fetch() { # groupId path artifact version
  local coord="$1" out="$2"
  if [ -f "$out" ]; then echo "skip  $out (exists)"; return; fi
  local url="https://repo1.maven.org/maven2/${coord}"
  echo "fetch $out"
  curl -fsSL -o "$out" "$url"
}

fetch com/mysql/mysql-connector-j/8.0.33/mysql-connector-j-8.0.33.jar \
      mysql-connector-j-8.0.33.jar
fetch org/postgresql/postgresql/42.7.13/postgresql-42.7.13.jar \
      postgresql-42.7.13.jar
fetch com/oceanbase/oceanbase-client/2.4.1/oceanbase-client-2.4.1.jar \
      oceanbase-client-2.4.1.jar
fetch com/oracle/database/jdbc/ojdbc8/23.26.3.0.0/ojdbc8-23.26.3.0.0.jar \
      ojdbc8-23.26.3.0.0.jar

echo "done. 用法：owljdbc.Config.Classpath 指向这些 jar；jars_dir 指向本目录。"
