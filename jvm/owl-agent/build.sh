#!/usr/bin/env bash
set -eu
cd "$(dirname "$0")"
rm -rf out owl-agent.jar
mkdir -p out
# 用 shell glob 而非 find：GitHub Actions 的 Windows `shell: bash` 以
# --noprofile 启动 Git Bash，Git 的 usr/bin 不在 PATH，find 会落到系统
# find.exe 而列出空文件集。glob 由 bash 自身展开，三平台一致。
javac --release 8 -d out src/owl/agent/*.java
jar cfe owl-agent.jar owl.agent.Main -C out .
echo "built owl-agent.jar"
