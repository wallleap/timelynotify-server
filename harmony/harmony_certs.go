package harmony

// Huawei Push Kit Service Account Credentials
//
// This file stores the credentials required to authenticate with Huawei's
// Push Kit REST API using the "Service Account JWT" method (recommended
// for HarmonyOS NEXT applications).
//
// ============================================================
//  ⚠️  SECURITY WARNING
//  Do NOT commit real credentials to a public repository.
//  The placeholder values below are intentionally invalid so that
//  the service fails fast at startup with a clear error message.
// ============================================================
//
// Setup Steps:
//
//  1. Go to the Huawei Developer API Console:
//     https://developer.huawei.com/consumer/cn/console/api/myApi
//
//  2. Click "服务账号密钥" (Service Account Key) → "创建凭证"
//     - Operation type: 新建服务账号
//     - Role: 管理员 (required for Push Kit access)
//     - Click "生成公私钥" to auto-generate the key pair
//
//  3. Click "创建并下载 JSON" to download the credentials file.
//     ⚠️  The private key is only shown ONCE. Store it securely.
//
//  4. Open the downloaded JSON file and copy each field into the
//     corresponding variables below:
//
//     JSON field         →  Go variable          →  Description
//     ---------------------------------------------------------------------------
//     key_id             →  keyID                →  Key identifier from
//                                                   the service account
//     sub_account        →  subAccount           →  Service account ID
//     project_id         →  projectID            →  Project ID (used to
//                                                   construct the API URL)
//     private_key        →  privateKey           →  Full RSA private key
//                                                   (keep the PEM format
//                                                   with \n escape sequences
//                                                   or actual newlines)
//
//  5. Save this file and restart the server.
//     The startup log should show: "HarmonyOS push client initialized"
//
// Testing with Mock Server (local development only):
//   Set BARK_SERVER_HARMONY_MOCK_URL=http://localhost:9999 to point the
//   client at a local mock server instead of Huawei's production API.
//   Run `python3 scripts/mock_huawei.py 9999` to start the mock.

var (
	// keyID: The key_id field from the downloaded credentials JSON.
	// Example: "6569cee5fdd84a5798f20ef3b1d82c0d"
	keyID = "6569cee5fdd84a5798f20ef3b1d82c0d"

	// subAccount: The sub_account field from the credentials JSON.
	// This is used as the JWT "iss" (issuer) claim.
	// Example: "118722123"
	subAccount = "118722123"

	// projectID: The project_id field from the credentials JSON.
	// This is used to construct the push API endpoint URL:
	//   https://push-api.cloud.huawei.com/v1/<projectID>/messages:send
	// Example: "461323198428915726"
	projectID = "461323198428915726"

	// privateKey: The private_key field from the credentials JSON.
	// This is an RSA private key in PKCS#8 PEM format.
	//
	// IMPORTANT FORMATS SUPPORTED:
	//   - Single-line with literal \n escape sequences (as copied from JSON)
	//     e.g. "-----BEGIN PRIVATE KEY-----\nMIIJ...\n-----END PRIVATE KEY-----"
	//   - Multi-line with actual newlines (standard PEM format)
	//
	// Both formats are accepted. The code normalizes \n to actual newlines
	// before PEM decoding.
	privateKey = `-----BEGIN PRIVATE KEY-----
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
-----END PRIVATE KEY-----`
)
