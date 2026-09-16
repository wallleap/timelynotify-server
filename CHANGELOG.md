<a name="unreleased"></a>

## [v0.7.0](https://github.com/wallleap/timelynotify-server/compare/v0.6.0...v0.7.0)

> 2026-09-15

### Documentation

- 同步文档与实际行为

### Features

- **gotify:** 实现消息增量同步（after 参数与删除事件流水）
- **harmony:** 支持点击跳转链接


## [v0.6.0](https://github.com/wallleap/timelynotify-server/compare/v0.5.3...v0.6.0)

> 2026-09-03

### Documentation

- update CHANGELOG for v0.6.0

### Features

- 支持 HarmonyOS V3 收件箱样式与通知覆盖，及监控流 id 覆盖
- 新增鸿蒙平台通知音时长参数及跨平台音轨适配
- 全链路请求追踪与日志安全增强
- add badge support for APNs and Harmony push
- **harmony:** 实现鸿蒙通知撤回功能
- **harmony:** 新增华为推送V3收件箱样式通知支持
- **harmony:** add foregroundShow parameter for HarmonyOS V3 notifications
- **harmony:** 新增通知大图标支持，优化图标参数处理

### Maintenance

- 将 .trae/ 目录加入 gitignore


## [v0.5.3](https://github.com/wallleap/timelynotify-server/compare/v0.5.2...v0.5.3)

> 2026-09-01

### Documentation

- update CHANGELOG for v0.5.3
- update README with various changes and fixes

### Maintenance

- **deploy:** update docker-compose configs with sample env vars


## [v0.5.2](https://github.com/wallleap/timelynotify-server/compare/v0.5.1...v0.5.2)

> 2026-08-31

### Bug Fixes

- **database:** 修复legacy设备记录重复推送和残留问题
- **push:** 修复鸿蒙空标题 fallback 逻辑
- **route_push:** 适配华为V3推送需要非空标题的要求

### Documentation

- update CHANGELOG for v0.5.2
- update compatibility and differences docs
- 更新全量文档以适配多平台扇出推送逻辑
- 完善文档并新增API文档
- **gotify-compat:** update gotify兼容说明，补充basic auth下的头信息注意事项

### Features

- 实现多平台推送支持，重构数据库层与推送逻辑
- **gotify:** add message search and streaming export support
- **route_push:** 新增扫描探针路径的静默404处理逻辑

### Tests

- **auth:** add route auth related test cases


## [v0.5.1](https://github.com/wallleap/timelynotify-server/compare/v0.5.0...v0.5.1)

> 2026-08-25

### Bug Fixes

- **harmony:** 更新华为推送API版本至v3并完善JWT头配置

### Documentation

- update CHANGELOG for v0.5.1
- add global /version endpoint for client identity check

### Features

- **route:** add /version endpoint for server version check


## [v0.5.0](https://github.com/wallleap/timelynotify-server/compare/v0.4.0...v0.5.0)

> 2026-08-22

### Documentation

- update CHANGELOG for v0.5.0
- note push_test deviceToken requirement
- sync Basic Auth whitelist (/info) across README, TOKENS, DIFFERENCES and review notes
- **api:** document auth schemes, full Basic Auth whitelist and /metrics endpoint

### Features

- 新增鸿蒙(HarmonyOS)原生推送支持

### Maintenance

- 重命名项目为 timelynotify-server

### Tests

- **authfree:** cover healthz, lookalikes, case, trailing-slash and prefix variants
- **gotifycompat:** cover device-scoped delete, token compare and persist-error paths
- **push:** stub APNs seek to run push tests offline and cover edge/error cases


## [v0.4.0](https://github.com/wallleap/timelynotify-server/compare/v0.3.0...v0.4.0)

> 2026-08-08

### Bug Fixes

- **auth:** whitelist device-scoped DELETE /:device_key/message/:id from Basic Auth
- **database:** reject empty MySQL device token like bbolt
- **ratelimit:** evict idle keys to bound memory growth
- **router:** log auth-rejected requests by mounting logger before Basic Auth

### Documentation

- update CHANGELOG for v0.4.0
- **readme:** clarify Basic Auth request header for non-whitelisted paths

### Features

- **auth:** whitelist /info from Basic Auth, gate device count behind valid creds


## [v0.3.0](https://github.com/wallleap/timelynotify-server/compare/v0.2.2...v0.3.0)

> 2026-08-08

### Bug Fixes

- **database:** honor a provided device key in bbolt, matching MySQL
- **ratelimit:** pass through /register when rate limiting is disabled

### Build

- **docker:** drop gcc from builder image

### Documentation

- update CHANGELOG for v0.3.0
- update Gotify/MCP interface descriptions and bridge device_key config
- note that bridge monitors all messages (personal-deploy only)
- correct bin/release usage description in AGENTS.md

### Features

- **gotify:** add device-scoped /:device_key/{version,message,stream} interfaces

### Maintenance

- record no-auto-commit rule in AGENTS.md
- show live bouncing progress while building in bin/build
- print build progress in bin/build

### Tests

- **gotify:** cover device-scoped limit, ordering and SourceDevice precedence


## [v0.2.2](https://github.com/wallleap/timelynotify-server/compare/v0.2.1...v0.2.2)

> 2026-08-07

### Bug Fixes

- write CHANGELOG via stdout and brace-quote NEW_TAG in release

### Documentation

- update CHANGELOG for v0.2.2


## [v0.2.1](https://github.com/wallleap/timelynotify-server/compare/v0.2.0...v0.2.1)

> 2026-08-07

### Bug Fixes

- locate git-chglog via GOPATH/bin when not on PATH
- base version bump on most recent release tag to support cross-MAJOR

### Build

- add bin/release for version bump, changelog and tag push
- add git-chglog for CHANGELOG generation


## [v0.2.0](https://github.com/wallleap/timelynotify-server/compare/v0.1.0...v0.2.0)

> 2026-08-07

### Bug Fixes

- only expose device count on /info when Basic Auth is enabled
- surface prominent warning when Basic Auth is disabled

### Build

- add PVC persistence and MySQL credentials via Secret in Helm
- run container as non-root user and fix entrypoint startup

### Documentation

- note sudo chown 1000:1000 for host-mounted data dir
- document log level/format and Prometheus metrics (2.2)
- add analysis notes for 5.7 log redaction and 5.8 replica consistency
- mark 5.4 info device-count leak resolved
- document gotify-max-messages and mark 5.3 done
- mark completed optimization review items as done
- document actual env vars for addr and data in README
- document rate limit, non-root container and Helm in AGENTS.md
- document rate limit options, security guidance and non-root
- add optimization review with additional findings
- remove dockerhub overview workflow and file
- add Docker Hub overview sync and docker run restart policy
- expand API_V2 doc with new fields, batch push, response and auth notes
- fix gotify_url example port in GOTIFY_COMPAT.md

### Features

- wire log level, Prometheus metrics and /metrics endpoint into server
- make gotify max messages configurable
- add per-IP rate limiting for register and mcp endpoints

### Maintenance

- show gotify-max-messages in docker compose examples

### Tests

- add runs-now-apns and database package tests


## v0.1.0

> 2026-08-07

### Bug Fixes

- correct error status code for URL path parsing failure
- drop request body from access logs
- keep client token out of access logs
- harden apns error handling and basic auth whitelist
- ensure gotify client token is printed on first boot
- [#66](https://github.com/wallleap/timelynotify-server/issues/66)
- increase maximum device token length
- return error for invalid device token removal
- mask token query param in access logs
- :bug: fix docker build faild
- **mcp:** disable SSE streaming to mitigate FIN-WAIT-2 buildup via docker-proxy
- **register_check:** remove url param
- **server:** fix ping msg
- **url:** fix compat url code
- **v2:** fix device key missing
- **v2:** fix device token missing
- **x509:** fix load system cert pool crash on windows

### Build

- accelerate apk with Alpine mirror (default aliyun)
- **ci:** push tagged images via secrets and add semver docs

### Documentation

- how to view the auto-generated client token via docker logs
- update log policy in AGENTS.md
- record log redaction scope in AGENTS.md
- recommend header-based token and TLS in production
- add quick-start tutorial and drop README TODO list
- mark delete endpoints in README and GOTIFY_COMPAT
- strongly recommend presetting gotify client token
- refresh AGENTS.md with testing and constraints
- add token concept guide (TOKENS.md)
- add GitHub Container Registry image usage instructions
- **API_V2.md:** add url field description
- **README:** update README
- **README:** update README
- **README:** remove docker image version tag
- **README:** update README.md
- **README:** update README.md
- **README:** update README.md
- **README:** update README
- **README:** add toc
- **README:** update README
- **README:** update README
- **README:** update README.md
- **README:** update docker compose command
- **apns:** add comment
- **authkey:** add pem format
- **readme:** update README.MD
- **v2:** add v2 api doc
- **v2:** update v2 api doc
- **v2:** update doc

### Features

- 优化变量名称
- optimize the use of alpine apk command
- add gotify-compatible monitoring interface for hotify-bridge
- add additional notification options
- 优化代码
- 优化 时区设置
- add apns push msg payload size check
- add delete message endpoints to gotify compat
- rebrand finb/bark-server fork as hotify-bark-server
- 添加并设置时区为中国时区
- 优化时区设置
- **apns:** add apple CAs
- **apns:** mv apns to new pkg
- **apns:** add private key
- **auth:** add basic auth support
- **cert:** add GeoTrust Global CA
- **comapt:** update compat route weight
- **compat:** update compat request
- **data:** support custom bark server db path
- **docker:** add docker compose support
- **graceful-shutdown:** add db close
- **http:** add https support
- **http:** change http framework to fiber
- **http:** remove pre fork support
- **info:** add number of devices
- **log:** update log format
- **log:** update log format
- **register:** add register check
- **register:** remove uuid, fix query parse
- **route:** update register check route
- **router:** update log format
- **v2:** tmp commit
- **version:** Bump v2.0.2

### Maintenance

- **build:** remove freebsd pre build
- **build:** add Apple M1 support
- **build:** add deploy file to release
- **build:** fix cross compile
- **build:** update build file name
- **buildx:** add binfmt install
- **buildx:** add buildx support
- **ci:** update build config, add version cmd
- **ci:** add GOAMD64 support
- **ci:** fix docker build cache
- **ci:** fix ca-certificates
- **ci:** remove linux/386
- **ci:** add taskfile
- **deploy:** add cert
- **deploy:** update compose version
- **deps:** bump github.com/gofiber/fiber/v2 from 2.40.0 to 2.40.1
- **deps:** bump github.com/gofiber/fiber/v2 from 2.52.7 to 2.52.9
- **deps:** bump github.com/urfave/cli/v2 from 2.17.1 to 2.19.2
- **deps:** bump github.com/urfave/cli/v2 from 2.27.3 to 2.27.4
- **deps:** bump golang.org/x/net from 0.27.0 to 0.28.0
- **deps:** bump github.com/urfave/cli/v2 from 2.27.2 to 2.27.3
- **deps:** bump golang.org/x/net from 0.26.0 to 0.27.0
- **deps:** bump github.com/gofiber/fiber/v2 from 2.52.3 to 2.52.5
- **deps:** bump golang.org/x/net from 0.25.0 to 0.26.0
- **deps:** bump golang.org/x/net from 0.24.0 to 0.25.0
- **deps:** bump go.etcd.io/bbolt from 1.3.9 to 1.3.10
- **deps:** bump github.com/urfave/cli/v2 from 2.27.1 to 2.27.2
- **deps:** bump golang.org/x/net from 0.22.0 to 0.24.0
- **deps:** bump github.com/go-sql-driver/mysql from 1.8.0 to 1.8.1
- **deps:** bump github.com/gofiber/fiber/v2 from 2.52.2 to 2.52.3
- **deps:** bump github.com/go-sql-driver/mysql from 1.7.1 to 1.8.0
- **deps:** bump golang.org/x/net from 0.21.0 to 0.22.0
- **deps:** bump github.com/gofiber/fiber/v2 from 2.52.1 to 2.52.2
- **deps:** bump go.etcd.io/bbolt from 1.3.8 to 1.3.9
- **deps:** bump github.com/gofiber/fiber/v2 from 2.52.0 to 2.52.1
- **deps:** bump golang.org/x/net from 0.20.0 to 0.21.0
- **deps:** bump golang.org/x/net from 0.19.0 to 0.20.0
- **deps:** bump github.com/gofiber/fiber/v2 from 2.51.0 to 2.52.0
- **deps:** bump github.com/urfave/cli/v2 from 2.27.0 to 2.27.1
- **deps:** bump github.com/urfave/cli/v2 from 2.26.0 to 2.27.0
- **deps:** bump github.com/urfave/cli/v2 from 2.25.7 to 2.26.0
- **deps:** bump golang.org/x/net from 0.18.0 to 0.19.0
- **deps:** bump github.com/gofiber/fiber/v2 from 2.50.0 to 2.51.0
- **deps:** bump golang.org/x/net from 0.17.0 to 0.18.0
- **deps:** bump go.etcd.io/bbolt from 1.3.7 to 1.3.8
- **deps:** bump github.com/gofiber/fiber/v2 from 2.49.2 to 2.50.0
- **deps:** bump golang.org/x/net from 0.16.0 to 0.17.0
- **deps:** bump golang.org/x/net from 0.15.0 to 0.16.0
- **deps:** bump github.com/gofiber/fiber/v2 from 2.49.1 to 2.49.2
- **deps:** bump github.com/gofiber/fiber/v2 from 2.49.0 to 2.49.1
- **deps:** bump golang.org/x/net from 0.14.0 to 0.15.0
- **deps:** bump github.com/gofiber/fiber/v2 from 2.48.0 to 2.49.0
- **deps:** bump golang.org/x/net from 0.13.0 to 0.14.0
- **deps:** bump golang.org/x/net from 0.12.0 to 0.13.0
- **deps:** bump github.com/gofiber/fiber/v2 from 2.47.0 to 2.48.0
- **deps:** bump golang.org/x/net from 0.11.0 to 0.12.0
- **deps:** bump github.com/buger/jsonparser from 1.1.1 to 1.1.2
- **deps:** bump github.com/urfave/cli/v2 from 2.25.6 to 2.25.7
- **deps:** bump github.com/gofiber/fiber/v2 from 2.46.0 to 2.47.0
- **deps:** bump golang.org/x/net from 0.10.0 to 0.11.0
- **deps:** bump github.com/urfave/cli/v2 from 2.25.5 to 2.25.6
- **deps:** bump github.com/urfave/cli/v2 from 2.25.4 to 2.25.5
- **deps:** bump github.com/urfave/cli/v2 from 2.25.3 to 2.25.4
- **deps:** bump github.com/gofiber/fiber/v2 from 2.45.0 to 2.46.0
- **deps:** bump golang.org/x/net from 0.9.0 to 0.10.0
- **deps:** bump github.com/gofiber/fiber/v2 from 2.44.0 to 2.45.0
- **deps:** bump github.com/urfave/cli/v2 from 2.25.2 to 2.25.3
- **deps:** bump github.com/urfave/cli/v2 from 2.25.0 to 2.25.2
- **deps:** bump github.com/go-sql-driver/mysql from 1.7.0 to 1.7.1
- **deps:** bump github.com/gofiber/fiber/v2 from 2.43.0 to 2.44.0
- **deps:** bump golang.org/x/net from 0.8.0 to 0.9.0
- **deps:** bump github.com/gofiber/fiber/v2 from 2.42.0 to 2.43.0
- **deps:** bump github.com/urfave/cli/v2 from 2.24.4 to 2.25.0
- **deps:** bump golang.org/x/net from 0.7.0 to 0.8.0
- **deps:** bump golang.org/x/net
- **deps:** bump golang.org/x/text from 0.3.7 to 0.3.8
- **deps:** bump github.com/urfave/cli/v2 from 2.24.3 to 2.24.4
- **deps:** bump github.com/gofiber/fiber/v2 from 2.41.0 to 2.42.0
- **deps:** bump github.com/urfave/cli/v2 from 2.24.2 to 2.24.3
- **deps:** bump go.etcd.io/bbolt from 1.3.5 to 1.3.7
- **deps:** bump github.com/urfave/cli/v2 from 2.24.1 to 2.24.2
- **deps:** bump github.com/urfave/cli/v2 from 2.23.7 to 2.24.1
- **deps:** bump github.com/gofiber/fiber/v2 from 2.40.1 to 2.41.0
- **deps:** bump github.com/urfave/cli/v2 from 2.23.6 to 2.23.7
- **deps:** bump github.com/go-sql-driver/mysql from 1.6.0 to 1.7.0
- **deps:** bump github.com/urfave/cli/v2 from 2.23.5 to 2.23.6
- **deps:** bump golang.org/x/net from 0.54.0 to 0.55.0
- **deps:** bump github.com/urfave/cli/v2 from 2.23.4 to 2.23.5
- **deps:** bump github.com/gofiber/fiber/v2 from 2.39.0 to 2.40.0
- **deps:** bump github.com/urfave/cli/v2 from 2.20.3 to 2.23.4
- **deps:** bump github.com/urfave/cli/v2 from 2.20.2 to 2.20.3
- **deps:** bump github.com/gofiber/fiber/v2 from 2.38.1 to 2.39.0
- **deps:** bump github.com/urfave/cli/v2 from 2.19.2 to 2.20.2
- **deps:** bump github.com/sideshow/apns2 from 0.23.0 to 0.24.0
- **deps:** bump go.etcd.io/bbolt from 1.3.10 to 1.3.11
- **deps:** add deps bot
- **deps:** bump github.com/urfave/cli/v2 from 2.16.2 to 2.16.3
- **deps:** bump github.com/urfave/cli/v2 from 2.14.1 to 2.16.2
- **deps:** bump github.com/gofiber/fiber/v2 from 2.37.0 to 2.37.1
- **deps:** bump github.com/urfave/cli/v2 from 2.14.0 to 2.14.1
- **deps:** bump github.com/urfave/cli/v2 from 2.11.2 to 2.14.0
- **deps:** bump github.com/urfave/cli/v2 from 2.11.1 to 2.11.2
- **deps:** bump github.com/gofiber/fiber/v2 from 2.36.0 to 2.37.0
- **deps:** bump github.com/gofiber/fiber/v2 from 2.35.0 to 2.36.0
- **deps:** bump github.com/urfave/cli/v2 from 2.10.3 to 2.11.1
- **deps:** bump golang.org/x/net from 0.28.0 to 0.29.0
- **deps:** bump github.com/gofiber/fiber/v2 from 2.52.11 to 2.52.12
- **deps:** bump golang.org/x/net from 0.29.0 to 0.30.0
- **deps:** bump github.com/urfave/cli/v2 from 2.4.0 to 2.10.3
- **deps:** bump github.com/gofiber/fiber/v2 from 2.29.0 to 2.35.0
- **deps:** bump github.com/gofiber/fiber/v2 from 2.52.9 to 2.52.11
- **deps:** bump github.com/sideshow/apns2 from 0.22.0 to 0.23.0
- **deps:** bump github.com/sideshow/apns2 from 0.20.0 to 0.22.0
- **deps:** fix directory
- **deps:** check Dockerfile deps
- **deps:** bump github.com/mritd/logger from 0.0.5 to 0.0.6
- **deps:** bump github.com/gofiber/fiber/v2 from 2.20.0 to 2.29.0
- **deps:** bump github.com/urfave/cli/v2 from 2.3.0 to 2.4.0
- **deps:** bump github.com/gofiber/fiber/v2 from 2.17.0 to 2.20.0
- **deps:** bump github.com/json-iterator/go from 1.1.11 to 1.1.12
- **deps:** bump github.com/gofiber/fiber/v2 from 2.16.0 to 2.17.0
- **deps:** bump github.com/gofiber/fiber/v2 from 2.15.0 to 2.16.0
- **deps:** bump github.com/gofiber/fiber/v2 from 2.14.0 to 2.15.0
- **deps:** bump github.com/gofiber/fiber/v2 from 2.13.0 to 2.14.0
- **deps:** bump github.com/gofiber/fiber/v2 from 2.12.0 to 2.13.0
- **deps:** bump github.com/gofiber/fiber/v2 from 2.11.0 to 2.12.0
- **deps:** bump github.com/gofiber/fiber/v2 from 2.10.0 to 2.11.0
- **deps:** bump github.com/gofiber/fiber/v2 from 2.9.0 to 2.10.0
- **deps:** bump github.com/lithammer/shortuuid/v3 from 3.0.6 to 3.0.7
- **deps:** bump github.com/urfave/cli/v2 from 2.16.3 to 2.17.1
- **deps:** bump github.com/gofiber/fiber/v2 from 2.5.0 to 2.9.0
- **deps:** bump github.com/json-iterator/go from 1.1.9 to 1.1.11
- **deps:** bump github.com/gofiber/fiber/v2 from 2.52.6 to 2.52.7
- **deps:** bump github.com/gofiber/fiber/v2 from 2.37.1 to 2.38.1
- **deps:** bump golang.org/x/net from 0.36.0 to 0.38.0
- **deps:** bump github.com/golang-jwt/jwt/v4 from 4.5.1 to 4.5.2
- **deps:** bump golang.org/x/net from 0.34.0 to 0.36.0
- **deps:** bump github.com/gofiber/fiber/v2 from 2.52.5 to 2.52.6
- **deps:** bump github.com/sideshow/apns2 from 0.24.0 to 0.25.0
- **deps:** bump github.com/golang-jwt/jwt/v4 from 4.4.1 to 4.5.1
- **deps:** bump golang.org/x/net from 0.31.0 to 0.34.0
- **deps:** bump golang.org/x/net from 0.30.0 to 0.31.0
- **depsbot:** fix time
- **docker:** update dockerfile
- **docker:** fix dockerfile
- **docker:** update dockerfile
- **docker:** update base image
- **docker:** update base image
- **docker:** fix ci build failed
- **docker:** fix dockerfile
- **docker:** fix docker build
- **docker:** fix netgo
- **docker:** add trimpath
- **docker-compose:** update docker-compose
- **fiber:** update fiber to v2.9.0
- **git:** update git ignore
- **gomod:** update go mode dep
- **gox:** remove gox
- **make:** update make file, update docker base image
- **make:** update compile sh
- **make:** add pre release
- **make:** fix make release
- **make:** update makefile
- **make:** fix env
- **make:** update
- **mod:** update sum
- **mod:** update go mod
- **mod:** update go version
- **release:** fix release files
- **systemd:** add pre-fork option
- **task:** add release task
- **task:** add freebsd support

