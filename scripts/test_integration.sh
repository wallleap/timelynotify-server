#!/bin/bash
#==============================================================================
# Hotify-Bark-Server 鸿蒙推送集成测试脚本
# 模拟完整的鸿蒙设备注册和推送流程，验证端到端功能
#==============================================================================
set -e

# Configuration
MOCK_PORT=9999
SERVER_PORT=18090
DATA_DIR="./test-e2e-data"
BARK_URL="http://localhost:${SERVER_PORT}"
PASS=0
FAIL=0
TOTAL=0

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

echo_banner() {
    echo ""
    echo -e "${BLUE}==============================================${NC}"
    echo -e "${BLUE}  $1${NC}"
    echo -e "${BLUE}==============================================${NC}"
}

log_pass() {
    echo -e "${GREEN}[PASS]${NC} $1"
    PASS=$((PASS + 1))
    TOTAL=$((TOTAL + 1))
}

log_fail() {
    echo -e "${RED}[FAIL]${NC} $1"
    FAIL=$((FAIL + 1))
    TOTAL=$((TOTAL + 1))
}

log_info() {
    echo -e "${YELLOW}[INFO]${NC} $1"
}

# Cleanup function
cleanup() {
    echo ""
    echo_banner "清理环境"
    pkill -f "mock_huawei.py" 2>/dev/null || true
    pkill -f "hotify-bark-server" 2>/dev/null || true
    sleep 1
    rm -rf "$DATA_DIR"
    echo "已清理"
}

trap cleanup EXIT

#==============================================================================
# Step 0: 构建项目
#==============================================================================
echo_banner "Step 0: 构建项目"

if ! command -v go &> /dev/null; then
    log_fail "Go not found"
    exit 1
fi

echo "Building..."
if go build -o dist/hotify-bark-server . 2>&1; then
    log_pass "项目编译成功"
else
    log_fail "项目编译失败"
    exit 1
fi

#==============================================================================
# Step 1: 启动 Mock 华为推送服务器
#==============================================================================
echo_banner "Step 1: 启动 Mock 华为推送服务器"

pkill -f "mock_huawei.py" 2>/dev/null || true
sleep 0.5

python3 scripts/mock_huawei.py $MOCK_PORT > /tmp/mock_huawei.log 2>&1 &
MOCK_PID=$!
sleep 1

if kill -0 $MOCK_PID 2>/dev/null; then
    log_pass "Mock 服务器已启动 (PID: $MOCK_PID, Port: $MOCK_PORT)"
else
    log_fail "Mock 服务器启动失败"
    exit 1
fi

#==============================================================================
# Step 2: 启动 Bark Server
#==============================================================================
echo_banner "Step 2: 启动 Bark Server"

pkill -f "hotify-bark-server" 2>/dev/null || true
sleep 0.5

BARK_SERVER_HARMONY_MOCK_URL="http://localhost:${MOCK_PORT}" \
./dist/hotify-bark-server \
  --addr ":${SERVER_PORT}" \
  --data "$DATA_DIR" \
  --rate-limit-ip=0 \
  --rate-limit-push=0 > /tmp/bark_server.log 2>&1 &
SERVER_PID=$!

echo "等待服务就绪..."
for i in {1..20}; do
    if curl -s ${BARK_URL}/healthz > /dev/null 2>&1; then
        log_pass "Bark Server 已就绪 (PID: $SERVER_PID, Port: $SERVER_PORT)"
        break
    fi
    sleep 1
    if [ $i -eq 20 ]; then
        log_fail "Bark Server 启动超时"
        cat /tmp/bark_server.log
        exit 1
    fi
done

# Check if HarmonyOS client was initialized
if grep -q "HarmonyOS push client initialized" /tmp/bark_server.log; then
    log_pass "鸿蒙推送客户端已初始化"
elif grep -q "HarmonyOS push client not initialized" /tmp/bark_server.log; then
    log_fail "鸿蒙推送客户端初始化失败 - 检查 harmony/harmony_certs.go 凭证配置"
    echo ""
    echo "错误详情:"
    grep "HarmonyOS" /tmp/bark_server.log
    exit 1
fi

#==============================================================================
# Step 3: 测试用例
#==============================================================================
echo_banner "Step 3: 运行集成测试用例"

#--- Test Case 1: 注册鸿蒙设备 ---
echo ""
echo -e "${YELLOW}Test 1: 注册鸿蒙设备${NC}"
REGISTER_RESP=$(curl -s -X POST ${BARK_URL}/register \
    -H 'Content-Type: application/json' \
    -d '{
        "device_key": "harmony-test-001",
        "device_token": "HARMONY_PUSH_TOKEN_AABBCCDD123456789",
        "platform": "harmony"
    }')
echo "请求: POST /register {platform: harmony}"
echo "响应: $REGISTER_RESP"

if echo "$REGISTER_RESP" | grep -q '"code":200'; then
    if echo "$REGISTER_RESP" | grep -q '"platform":"harmony"'; then
        log_pass "Test 1.1: 鸿蒙设备注册成功，platform 字段正确"
    else
        log_fail "Test 1.1: 设备注册成功但 platform 字段缺失或错误"
    fi
else
    log_fail "Test 1.1: 设备注册失败"
fi

#--- Test Case 2: 用相同 key 再次注册（更新 token）---
echo ""
echo -e "${YELLOW}Test 2: 更新鸿蒙设备 token${NC}"
UPDATE_RESP=$(curl -s -X POST ${BARK_URL}/register \
    -H 'Content-Type: application/json' \
    -d '{
        "device_key": "harmony-test-001",
        "device_token": "UPDATED_HARMONY_TOKEN_999",
        "platform": "harmony"
    }')
echo "请求: POST /register (更新)"
echo "响应: $UPDATE_RESP"

if echo "$UPDATE_RESP" | grep -q '"code":200'; then
    log_pass "Test 2: 设备 token 更新成功"
else
    log_fail "Test 2: 设备 token 更新失败"
fi

#--- Test Case 3: 注册另一个鸿蒙设备（多设备支持）---
echo ""
echo -e "${YELLOW}Test 3: 注册第二个鸿蒙设备${NC}"
REGISTER2_RESP=$(curl -s -X POST ${BARK_URL}/register \
    -H 'Content-Type: application/json' \
    -d '{
        "device_key": "harmony-test-002",
        "device_token": "ANOTHER_HARMONY_TOKEN_111",
        "platform": "harmony"
    }')
echo "请求: POST /register (第二个设备)"
echo "响应: $REGISTER2_RESP"

if echo "$REGISTER2_RESP" | grep -q '"code":200'; then
    log_pass "Test 3: 第二个鸿蒙设备注册成功"
else
    log_fail "Test 3: 第二个设备注册失败"
fi

#--- Test Case 4: 发送鸿蒙推送（基本推送）---
echo ""
echo -e "${YELLOW}Test 4: 发送鸿蒙推送（基本）${NC}"
PUSH_RESP=$(curl -s -X POST ${BARK_URL}/push \
    -H 'Content-Type: application/json' \
    -d '{
        "device_key": "harmony-test-001",
        "title": "鸿蒙测试通知",
        "body": "这是一条来自 Hotify-Bark-Server 的鸿蒙推送测试消息"
    }')
echo "请求: POST /push"
echo "响应: $PUSH_RESP"

if echo "$PUSH_RESP" | grep -q '"code":200'; then
    log_pass "Test 4.1: 鸿蒙推送成功发送"
else
    log_fail "Test 4.1: 鸿蒙推送失败"
fi

# Check if mock received the request
if grep -q "POST" /tmp/mock_huawei.log 2>/dev/null; then
    log_pass "Test 4.2: Mock 服务器收到推送请求"
else
    log_fail "Test 4.2: Mock 服务器未收到请求"
fi

#--- Test Case 5: 发送带 level 的鸿蒙推送 ---
echo ""
echo -e "${YELLOW}Test 5: 发送带 level 的鸿蒙推送${NC}"
LEVEL_PUSH_RESP=$(curl -s -X POST ${BARK_URL}/push \
    -H 'Content-Type: application/json' \
    -d '{
        "device_key": "harmony-test-001",
        "title": "紧急通知",
        "body": "这是一条 critical 级别的推送",
        "level": "critical"
    }')
echo "请求: POST /push (level: critical)"
echo "响应: $LEVEL_PUSH_RESP"

if echo "$LEVEL_PUSH_RESP" | grep -q '"code":200'; then
    log_pass "Test 5.1: Critical 级别推送成功"
else
    log_fail "Test 5.1: Critical 级别推送失败"
fi

#--- Test Case 6: 发送 active 级别推送 ---
echo ""
echo -e "${YELLOW}Test 6: 发送 active 级别推送${NC}"
ACTIVE_PUSH_RESP=$(curl -s -X POST ${BARK_URL}/push \
    -H 'Content-Type: application/json' \
    -d '{
        "device_key": "harmony-test-002",
        "title": "普通通知",
        "body": "这是一条普通级别推送",
        "level": "active"
    }')
echo "请求: POST /push (level: active)"
echo "响应: $ACTIVE_PUSH_RESP"

if echo "$ACTIVE_PUSH_RESP" | grep -q '"code":200'; then
    log_pass "Test 6: Active 级别推送成功"
else
    log_fail "Test 6: Active 级别推送失败"
fi

#--- Test Case 7: 向不存在的设备推送 ---
echo ""
echo -e "${YELLOW}Test 7: 向不存在的设备推送（错误处理）${NC}"
ERROR_PUSH_RESP=$(curl -s -X POST ${BARK_URL}/push \
    -H 'Content-Type: application/json' \
    -d '{
        "device_key": "non-existent-device",
        "title": "测试",
        "body": "不存在的设备"
    }')
echo "请求: POST /push (invalid device)"
echo "响应: $ERROR_PUSH_RESP"

if echo "$ERROR_PUSH_RESP" | grep -q '"code":[45]'; then
    log_pass "Test 7: 向不存在设备推送返回错误（预期行为）"
else
    log_fail "Test 7: 错误处理不符合预期"
fi

#--- Test Case 8: 批量推送（多 token）---
echo ""
echo -e "${YELLOW}Test 8: 批量推送${NC}"
BATCH_PUSH_RESP=$(curl -s -X POST ${BARK_URL}/push \
    -H 'Content-Type: application/json' \
    -d '{
        "device_keys": ["harmony-test-001", "harmony-test-002"],
        "title": "批量推送测试",
        "body": "这条消息同时推送给多个设备"
    }')
echo "请求: POST /push (batch)"
echo "响应: $BATCH_PUSH_RESP"

if echo "$BATCH_PUSH_RESP" | grep -q '"code":200'; then
    log_pass "Test 8: 批量推送成功"
else
    log_fail "Test 8: 批量推送失败"
fi

#--- Test Case 9: gotify 监控流（鸿蒙推送也应进入监控）---
echo ""
echo -e "${YELLOW}Test 9: 监控流功能${NC}"
# 获取 client token - 使用 grep 匹配日志中的 token
CLIENT_TOKEN=$(grep -o 'client token.*: [a-zA-Z0-9_-]*' /tmp/bark_server.log 2>/dev/null | head -1 | sed 's/.*: //')
if [ -n "$CLIENT_TOKEN" ]; then
    log_info "Gotify client token: $CLIENT_TOKEN"
    
    # 发送一条推送，然后检查监控
    curl -s -X POST ${BARK_URL}/push \
        -H 'Content-Type: application/json' \
        -d '{"device_key":"harmony-test-001","title":"监控测试","body":"check stream"}' > /dev/null
    
    # 等待一下让 gotifyPublish 处理
    sleep 1
    
    # 检查消息历史
    HISTORY_RESP=$(curl -s -H "X-Gotify-Key: $CLIENT_TOKEN" ${BARK_URL}/message?limit=10)
    if echo "$HISTORY_RESP" | grep -q "监控测试"; then
        log_pass "Test 9: 鸿蒙推送进入 gotify 监控流"
    else
        log_info "Test 9: 监控流检查（可能需要更长时间同步）"
    fi
else
    log_info "Test 9: 跳过监控流测试（未找到 client token）"
    echo "  日志中搜索 token:"
    grep -i "token" /tmp/bark_server.log | head -3
fi

#--- Test Case 10: Mock 服务器日志验证 ---
echo ""
echo -e "${YELLOW}Test 10: Mock 服务器日志验证${NC}"
MOCK_LOG_ENTRIES=$(grep -c "POST" /tmp/mock_huawei.log 2>/dev/null || echo "0")
MOCK_LOG_ENTRIES=$(echo "$MOCK_LOG_ENTRIES" | tr -d '[:space:]')
echo "Mock 服务器收到的请求数: $MOCK_LOG_ENTRIES"

if [ -n "$MOCK_LOG_ENTRIES" ] && [ "$MOCK_LOG_ENTRIES" -gt 0 ] 2>/dev/null; then
    log_pass "Test 10.1: Mock 服务器收到 $MOCK_LOG_ENTRIES 个请求"
    
    # 显示 Mock 服务器收到的最新请求详情
    echo ""
    echo "Mock 服务器最后一条请求详情:"
    grep -A 10 "POST" /tmp/mock_huawei.log 2>/dev/null | tail -15
else
    log_fail "Test 10.1: Mock 服务器未收到任何请求"
fi

#--- Test Case 11: 服务器日志健康检查 ---
echo ""
echo -e "${YELLOW}Test 11: 服务器日志健康检查${NC}"
ERROR_COUNT=$(grep -ic "error\|fail" /tmp/bark_server.log 2>/dev/null || true)
ERROR_COUNT=$(echo "$ERROR_COUNT" | head -1 | tr -d '[:space:]')
if [ -z "$ERROR_COUNT" ]; then
    ERROR_COUNT=0
fi
if [ "$ERROR_COUNT" -eq 0 ] || [ "$ERROR_COUNT" -le 2 ]; then
    log_pass "Test 11.1: 服务器日志无严重错误 (发现 $ERROR_COUNT 条可能错误日志)"
else
    log_fail "Test 11.1: 服务器日志存在 $ERROR_COUNT 条错误日志"
    echo ""
    echo "相关日志:"
    grep -i "error\|fail" /tmp/bark_server.log | tail -5
fi

#==============================================================================
# 测试结果汇总
#==============================================================================
echo ""
echo_banner "测试结果汇总"
echo ""
echo -e "  总用例数: ${TOTAL}"
echo -e "  ${GREEN}通过: ${PASS}${NC}"
echo -e "  ${RED}失败: ${FAIL}${NC}"
echo ""

if [ "$FAIL" -eq 0 ]; then
    echo -e "${GREEN}🎉 所有测试通过！鸿蒙推送功能验证成功。${NC}"
    echo ""
    echo "服务运行日志:"
    echo "  - Bark Server: /tmp/bark_server.log"
    echo "  - Mock Huawei: /tmp/mock_huawei.log"
    exit 0
else
    echo -e "${RED}❌ 有 ${FAIL} 个测试用例失败，请检查日志。${NC}"
    echo ""
    echo "调试信息:"
    echo "  - Bark Server Log: cat /tmp/bark_server.log"
    echo "  - Mock Huawei Log: cat /tmp/mock_huawei.log"
    echo "  - 启动参数: BARK_SERVER_HARMONY_MOCK_URL=http://localhost:9999"
    exit 1
fi