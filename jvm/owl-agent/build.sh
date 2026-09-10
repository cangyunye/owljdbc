#!/usr/bin/env bash
set -eu
cd "$(dirname "$0")"
rm -rf out owl-agent.jar
mkdir -p out
# 源码含中文注释，显式 -encoding UTF-8：Windows 上 javac 默认字符集非 UTF-8，
# 会按平台编码误读源码而编译失败（macOS/Linux 默认 UTF-8 故不暴露）。
# 用 shell glob 而非 find：Actions 的 Windows `shell: bash` 以 --noprofile
# 启动 Git Bash，Git 的 usr/bin 不在 PATH，find 会落到系统 find.exe。
javac -encoding UTF-8 --release 8 -d out src/owl/agent/*.java
jar cfe owl-agent.jar owl.agent.Main -C out .
echo "built owl-agent.jar"
