#!/bin/bash
# Quick local test for HarmonyOS push integration
set -e

MOCK_PORT=9999
SERVER_PORT=18080
DATA_DIR="./test-data"

echo "=============================================="
echo "  🚀 Hotify-Bark-Server 鸿蒙推送本地测试"
echo "=============================================="
echo ""

# Cleanup
cleanup() {
    echo ""
    echo "🛑 清理中..."
    kill $MOCK_PID 2>/dev/null || true
    kill $SERVER_PID 2>/dev/null || true
    wait $MOCK_PID 2>/dev/null || true
    wait $SERVER_PID 2>/dev/null || true
    rm -rf "$DATA_DIR"
    echo "✅ 清理完成"
    exit 0
}
trap cleanup INT TERM

# Step 1: Start Mock Huawei Server
echo "[1] 启动 Mock 华为推送服务器..."
python3 scripts/mock_huawei.py $MOCK_PORT > /tmp/mock_huawei.log 2>&1 &
MOCK_PID=$!
sleep 1
echo "  ✔ Mock 服务器运行在端口 $MOCK_PORT (PID: $MOCK_PID)"

# Step 2: Start Bark Server with mock URL
echo "[2] 启动 Bark Server (对接 Mock 服务)..."
BARK_SERVER_HARMONY_MOCK_URL="http://localhost:$MOCK_PORT" \
go run . --data "$DATA_DIR" > /tmp/bark_server.log 2>&1 &
SERVER_PID=$!

# Wait for server to be ready
echo "  ⏳ 等待服务就绪..."
for i in {1..15}; do
    if curl -s http://localhost:$SERVER_PORT/healthz > /dev/null 2>&1; then
        echo "  ✔ Bark Server 已就绪 (PID: $SERVER_PID)"
        break
    fi
    sleep 1
done

# Step 3: Test Register
echo ""
echo "[3] 测试：注册鸿蒙设备"
REGISTER_RESP=$(curl -s -X POST http://localhost:$SERVER_PORT/register \
    -H 'Content-Type: application/json' \
    -d '{"device_key":"test-harmony-001","device_token":"HARMONY_TEST_TOKEN_12345","platform":"harmony"}')
echo "  请求: POST /register"
echo "  响应: $REGISTER_RESP"
echo ""

# Step 4: Test Push
echo "[4] 测试：发送鸿蒙推送"
PUSH_RESP=$(curl -s -X POST http://localhost:$SERVER_PORT/push \
    -H 'Content-Type: application/json' \
    -d '{"device_key":"test-harmony-001","title":"测试通知","body":"这是一条鸿蒙测试推送!","level":"active"}')
echo "  请求: POST /push"
echo "  响应: $PUSH_RESP"
echo ""

# Step 5: Show Mock Logs
echo "[5] Mock 服务器日志 (最近的请求):"
echo "  ----------------------------------------"
cat /tmp/mock_huawei.log | grep -A 5 "POST" | tail -20 || echo "  (等待请求到达...)"
echo "  ----------------------------------------"

echo ""
echo "=============================================="
echo "  🎉 测试完成!"
echo "=============================================="
echo ""
echo "📊 服务日志位置:"
echo "  - Bark Server: /tmp/bark_server.log"
echo "  - Mock Huawei: /tmp/mock_huawei.log"
echo ""
echo "🔍 查看完整日志:"
echo "  tail -f /tmp/bark_server.log"
echo "  tail -f /tmp/mock_huawei.log"
echo ""
echo "按 Ctrl+C 停止所有服务并清理环境..."

# Keep running
wait
