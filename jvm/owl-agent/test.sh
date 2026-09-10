#!/usr/bin/env bash
set -eu
cd "$(dirname "$0")"
rm -rf out-test
mkdir -p out-test
javac --release 8 -d out-test \
  src/owl/agent/BindRewriter.java src/owl/agent/ValueCodec.java src/owl/agent/Json.java \
  src/owl/agent/test/BindRewriterTest.java src/owl/agent/test/ValueCodecTest.java src/owl/agent/test/JsonTest.java
java -cp out-test owl.agent.test.BindRewriterTest
java -cp out-test owl.agent.test.ValueCodecTest
java -cp out-test owl.agent.test.JsonTest
echo "ALL JAVA TESTS OK"
