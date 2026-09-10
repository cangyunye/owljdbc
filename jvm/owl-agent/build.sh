#!/usr/bin/env bash
set -eu
cd "$(dirname "$0")"
rm -rf out owl-agent.jar
mkdir -p out
javac --release 8 -d out $(find src/owl/agent -maxdepth 1 -name '*.java')
jar cfe owl-agent.jar owl.agent.Main -C out .
echo "built owl-agent.jar"
