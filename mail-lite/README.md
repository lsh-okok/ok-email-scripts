# Mail Lite

独立的轻量邮箱工作台：Go 主节点 / 子节点、SQLite WAL、Vue 3 + Naive UI 管理端，以及 Go 模板公共取信页。前端打包进程序，生产环境无需 Node.js、Redis 或独立调度器。全新数据模型，不兼容原 Python 项目的数据库、链接及 API。

此目录包含新版 Go / Vue 应用的构建源码，Linux 一键安装入口为仓库根目录的 `install-mail-lite.sh`。

## 启动

### Ubuntu / Debian 一键部署 / 更新

将当前项目上传到 Linux 服务器，在仓库根目录执行 `sudo bash install-mail-lite.sh`。默认通过服务器 IP 的 8080 端口访问；也可按提示选择 Caddy 自动 HTTPS 或接入现有 Nginx Proxy Manager，并选择是否启用 MX 收信。脚本自动安装缺失的 Docker / Compose、生成管理员密码、构建并检查服务；重复运行沿用服务器 `.env` 和数据，切换前自动备份。参数和示例见 [DEPLOYMENT.md](DEPLOYMENT.md)。

在 Windows 本机双击仓库根目录的 [`deploy.bat`](../deploy.bat)，首次填写 SSH 连接、代理模式、域名及 MX 选项；之后双击 [`update.bat`](../update.bat) 即可上传当前源码并更新。默认接入现有 Nginx Proxy Manager 的 Docker 网络，由 NPM 管理网站与证书；也可选择独立 Caddy HTTPS 或 HTTP 直连。脚本自动准备 Docker、更新前备份完整数据目录，并检查服务就绪状态。服务器配置及数据卷持续保留。使用方法、端口要求和备份位置见 [DEPLOYMENT.md](DEPLOYMENT.md)。

### Windows 一键启动 / 停止

在仓库根目录双击 [`start.bat`](../start.bat)，程序在后台启动并自动打开管理页面；双击 [`stop.bat`](../stop.bat) 停止。可以从任意工作目录调用这两个脚本，重复启动不会创建多个进程，重复停止也可正常退出。

脚本优先使用 `mail-lite/bin/mail-lite.exe`，不存在时自动用 Go 1.27+ 编译已有前端资源。首次使用新数据目录会要求输入至少 12 字符的管理员密码；已有数据库继续使用原密码。当前本机部署会沿用 `.cache/local-runtime.json` 中的地址与数据目录（`http://127.0.0.1:8081/admin/`）。没有已有配置时默认使用 `http://127.0.0.1:8080/admin/` 和 `mail-lite/data`。

可将 `mail-lite/.env.example` 复制为 `mail-lite/.env` 配置地址、数据目录和节点参数；填写自己的 `ADMIN_PASSWORD`，本机 HTTP 使用时将 `APP_ORIGIN` 留空。已有环境变量优先于 `.env`；存在 `.env` 时不再读取旧本机配置。配置采用 `NAME=value`，值可用单引号或双引号包裹，不执行变量展开。停止操作保留数据库和密钥。运行状态及日志位于 `mail-lite/.cache/`，日志为 `local-server.stdout.log`、`local-server.stderr.log`，每次启动覆盖。

```powershell
# 在仓库根目录执行；后台启动但不打开浏览器
.\start.bat -NoBrowser
.\stop.bat
# 修改 Go 代码后重新编译并启动（先停止）
.\start.bat -Build
```

修改前端后需先在 `mail-lite/web/ui` 执行 `npm run build`，再执行 `start.bat -Build`。

### 手动启动

需要 Go 1.27+。仓库已包含构建好的 `web/dist`，可直接编译：

```sh
go build -trimpath -ldflags="-s -w" -o bin/mail-lite ./cmd/mail-lite
ADMIN_PASSWORD='change-this-password' DATA_DIR=./data ./bin/mail-lite
```

PowerShell：

```powershell
$env:ADMIN_PASSWORD='change-this-password'
go build -trimpath -ldflags="-s -w" -o bin/mail-lite.exe ./cmd/mail-lite
.\bin\mail-lite.exe
```

打开 [http://localhost:8080/admin/](http://localhost:8080/admin/)。首次密码至少 12 个字符，最多 72 字节；密码哈希持久保存。可在“系统设置 → 管理员密码”中验证当前密码后修改，修改成功会退出所有设备上的管理台会话。网页修改后的密码在重启后继续有效，不会被未改变的 `ADMIN_PASSWORD` 覆盖；将 `ADMIN_PASSWORD` 改为不同于上次配置的值并重启，可重置密码并清除旧会话。自动生成的 `data/data.key` 必须随数据库一起保留；也可用 `DATA_KEY` 配置 base64url 编码的 32 字节密钥。不要在不同节点间共享数据目录。

Docker：复制 `.env.example` 为 `.env`，填写密码，执行 `docker compose up -d --build`。本机开发 HTTP 环境将 `APP_ORIGIN` 留空。镜像使用多阶段构建，运行时仅包含 Go 程序和 CA 证书。容器以 UID 10001 运行，数据位于 `/data`。Docker Compose 不会自动读取原项目配置。

生产环境使用 HTTPS：可将 8080 端口放在现有 Nginx/Caddy 反向代理后，配置 `APP_ORIGIN=https://mail.example.com`；代理应保留 Host。程序也支持同时指定 `TLS_CERT`、`TLS_KEY` 直接提供 HTTPS。不信任任意转发头，来源校验及 Secure Cookie 使用明确配置的 `APP_ORIGIN`。

## 管理操作

管理端提供首页、邮箱、链接、卡密管理、邮箱验证、节点和系统设置入口。系统设置集中管理公开根地址、邮件扫描数量、Token 刷新计划及管理员密码；手机底栏的“设置”同样可进入。所有管理操作支持手机，桌面为列表和抽屉、手机为卡片和全屏表单。浅色 / 深色 / 跟随系统共用本地浏览器设置，默认跟随系统。

### 卡密管理与批量兑换

`/admin/cards` 直接使用邮箱管理中的 Outlook / iCloud 主邮箱。一张卡密固定绑定一个主邮箱，支持勾选批量生成、复制、TXT 导出、停用与恢复；重复生成保留原卡密。MX 域名不是单个主邮箱，不参与卡密生成。

公开兑换页为 `/redeem`，无需管理员登录。可粘贴或导入 TXT，每次最多兑换 100 个卡密，重复输入自动去重；逐个显示结果，可重试失败项、复制和下载成功结果。兑换仅交付对应主邮箱下可用的子邮箱和取信链接，不交付主邮箱密码、Client ID 或 Token。没有可用链接时不会标记已兑换；重复兑换同一卡密可找回该主邮箱当前有效的链接。

链接继续遵守平台、有效期和主邮箱启停规则，优先使用系统设置中的公开根地址。停用卡密会暂停后续兑换查询；已获得的取信链接仍受链接自身规则控制。卡密加密存储在当前数据库，部署或恢复时需同时保留 `data.key`。卡密与兑换只在 primary 节点运行。

销售工作台入口和首页销售统计已移除，旧 `/admin/sales` 跳转到卡密管理。首次升级会一次性删除旧出库订单、资料快照、库存导入预览和销售活动记录，并清除预留、已售状态；邮箱、子邮箱链接、检测结果、SMTP 配置以及每日检测时间和范围保留。内部邮箱映射继续供验证使用，不需要库存导入或出库操作。

### 邮箱验证

`/admin/verification` 保留链接检测、SMTP 收信验证、每日检测计划、停止任务、详情、CSV 报告和未通过项复检。检测前可同步现有邮箱与链接，手动提交和每日执行也会同步新来源。可选择全部或指定主邮箱，设置每个主邮箱的抽样量、超时和并发等参数；停用、过期、已删除的来源自动排除，同邮箱或跨平台映射冲突会提示处理。

系统关联通过公共取信逻辑直接验证，支持本机地址且不发起回环 HTTP；自定义链接继续使用 HTTP 检测及内网访问防护。链接检测通过不代表公网可达或实际收信成功。收信验证会发送测试邮件，需先配置发件池并确认，且须找到本次测试标识；平台可能过滤测试邮件时会明确提示。定时检测使用系统设置中的时区，只在 primary 节点运行。

默认 SMTP 和发件池账号均可选择 163、126、Yeah、QQ/Foxmail、Yahoo、iCloud、Gmail、阿里个人邮箱预设，自动填入服务器、端口及加密方式，并显示授权码获取说明；支持自定义修改。iCloud 使用 587 + STARTTLS，其余预设使用 465 + SSL/TLS。手动检测和新建每日计划默认每个主邮箱抽样 1 个。

复检只处理仍可检测且链接和来源策略未变化的未通过项，任务详情保留原任务、本次复检数及跳过原因。收信复检继续使用原测试标识，不重新发信。检测详情和 CSV 报告均包含主邮箱。

### 邮箱、链接与节点

- **邮箱**：添加、搜索、分页、启用/停用、备注、三级分组，批量导入、刷新 Outlook Token。邮件默认加载最新 20 封，向下滚动继续加载，也可切换为从最早邮件开始；支持收件箱、垃圾箱和全部邮件。切换邮箱或离开页面后返回，会恢复本次登录中的列表、正文及滚动位置，点击刷新才重新取信。分组入口可设置继承代理，账号代理优先；支持 HTTP、HTTPS、SOCKS5，按连接使用，最多三个备用地址。URL 必须含端口，可含用户名密码。
- **链接**：搜索、按主邮箱或平台筛选，导入地址生成固定收件人的独立链接。平台规则匹配发件邮箱或域名（包含子域名），可附加主题关键词。有效期可选 1/3/7/15/30 天、长期有效或指定时间，天数从保存时计算。每页可显示 20/100/250/500/750 条；选择主邮箱后可跨页全选该邮箱当前筛选下的链接，也可逐页勾选。批量设置平台、有效期或删除最多处理 10000 条，作用于打开操作窗口时选中的 ID 快照；切换筛选清空选择。导出涵盖当前**全部筛选结果**。
- **节点**：创建独立凭据、查看心跳/游标/待同步数/错误，支持停用、启用、永久撤销及删除已撤销记录。临时下线使用“停用节点”，保留原凭据和配置；启用后运行中的子节点会自动重连。永久撤销后原凭据失效，恢复接入需要新凭据；可删除该记录以清理列表。凭据仅在创建时显示一次。常规设置已移至“系统设置”，包括公开根地址、每个文件夹扫描量及 Token 刷新调度。默认 `Asia/Shanghai` 每天 02:00 检查，距上次成功刷新满 30 天才刷新；手动刷新立即排队。

导入和刷新为后台任务，页面下方“最近任务”显示进度、失败数和最多 100 条错误。导入进度以输入行计数，一行可以包含多个地址。队列有界，重启后的未完成任务标记中断，可重新提交。链接每批 500 条事务提交；重复地址复用原链接、有效期及规则。重复主邮箱更新凭据；保留凭据请使用编辑表单留空。

邮箱列表只展示地址、类型、分组、绑定链接数、启停状态、最近刷新及错误；Token、Client ID 和应用专用密码不会出现在列表中。每行的 **详情** 打开邮箱详情抽屉，可查看、隐藏和复制该 Outlook 邮箱的完整配置（账号 ID、分组、备注、代理、Client ID、当前 Refresh Token、尚未过期的 Access Token 缓存）或 iCloud 应用专用密码。查看不会访问 Microsoft 或触发凭据轮换；关闭抽屉会移除界面中的凭据。Access Token 只有取信或刷新后才可能有缓存。

Access Token 缓存同时显示 Microsoft 响应中的实际权限范围 `scope`；接口未返回时显示“无法确认具体权限”，不会把请求的 scope 当成已授予权限。Graph 与 IMAP 使用不同资源的令牌，IMAP 取信成功不代表拥有 Graph 权限。授权失败会区分网络连接、Client ID、Refresh Token、权限范围及服务限流，并显示 Microsoft 的错误编号；两个协议均失败时保留各自原因。程序进程必须能连接 Microsoft 服务，本地健康检查通过只说明服务已启动。

授权与取信沿用原项目的令牌复用策略：普通取信缓存尚未过期的 Access Token，不保存 Microsoft 返回的轮换 Refresh Token；只有手动或定时刷新才保存轮换值。Graph 按原项目的权限候选顺序尝试，IMAP 使用消费者授权端点，并依次尝试 `outlook.live.com` 和 `outlook.office365.com`。切换排序、文件夹和加载下一页均不会主动强制刷新令牌。

IMAP 建连遇到短暂的网络中断、TLS 连接中断或服务器问候读取失败时，在同一次请求内间隔 150 毫秒自动重试一次，避免首屏正文连接偶发失败后必须手动刷新。重试遵守原请求的超时和取消，不强制刷新 Token；明确的授权拒绝、证书验证失败和永久 DNS 错误不重复连接。

顶部 **导出邮箱** 直接导出所有已导入邮箱的完整 TXT 导入信息，包含凭据并覆盖所有分页。Outlook 每行格式为 `邮箱----密码----Client ID----Refresh Token`，iCloud 每行格式为 `邮箱----应用专用密码`，导出的文件可直接重新导入。导出使用同一个数据库快照，包含该快照中的最新 Refresh Token。

凭据与邮箱导出接口分别为 `POST /api/admin/accounts/<id>/credentials` 和 `POST /api/admin/accounts/export`，仅主节点管理员可调用，要求登录 Cookie 与 `X-CSRF-Token`，响应禁止缓存。导出接口固定返回全部邮箱的完整 TXT 导入信息；普通邮箱列表不会返回 Refresh Token 或应用专用密码。

邮箱导入，每行一条，支持 TXT 多文件或粘贴：

```text
name@outlook.com----password----Client ID----Refresh Token
name@icloud.com----xxxx-xxxx-xxxx-xxxx
```

链接导入：先在页面筛选中选择主邮箱，每行填写一个子地址。未选主邮箱时，每个 TXT 的首行写已导入的主邮箱地址，或包含凭据的完整主邮箱行，后续为子地址。可用换行、逗号、空格或分号分隔子地址。支持 iCloud 隐藏邮箱转发和加号地址；主邮箱以应用专用密码通过 IMAP 连接。

```text
main@icloud.com
hidden@icloud.com
hidden+tag@icloud.com
```

子地址必须已由邮箱服务商配置好投递或转发；本程序只建立访问关系，不会在 Apple/Microsoft 端创建地址。

## 取信与删除

管理端列表接口为 `GET /api/admin/accounts/<id>/messages?limit=20&order=latest&folder=`，返回 `{items, order, folder, next_cursor, has_more, scan_limit}`。`limit` 默认及最大 20，`order` 可为 `latest` 或 `oldest`，`folder` 可为空、`inbox` 或 `junkemail`。后续请求携带 `cursor`，排序及文件夹必须与该游标一致。游标绑定账号和 generation，30 分钟后失效；服务重启或会话被回收后需刷新列表。Graph 分页位置和 IMAP UID 快照只保存在服务端，浏览器接收加密的会话引用。列表仅返回摘要，点击邮件后通过 `GET /api/admin/accounts/<id>/message?ref=...` 加载正文。

`GET /show/p0/<主邮箱10位随机标识>/<子邮箱@前的名称>` 显示最新一封，`?recent=1` 显示最多 20 封。主邮箱标识固定保存，编辑、重新导入及重启均不会改变。`GET /api/mailboxes/p0/<主邮箱10位随机标识>/<子邮箱名称>/messages?limit=1` 返回 `{recipient, items}`，limit 最大 20。`p0` 表示不限制平台，也可替换为后台生成的平台段。相同主邮箱下跨域同名的子邮箱短链接会拒绝取信，避免误读。原 `/m/<token>`、包含内部账号 ID 和完整邮箱地址的链接继续可用。每个链接固定主邮箱、收件人、平台和有效期，查询参数不能覆盖。子节点上的路径相同，只需替换域名。读取 JSON 时不要将邮件 HTML 直接插入自己的网页；内置页面使用隔离 iframe、CSP 并禁止远程图片与脚本。

停用主邮箱会拒绝所有公开取信入口，包括持有旧邮件 ID、`ref` 或分页游标的请求。指定平台的默认页先按收件人和平台规则筛选，再显示符合规则的最新一封。Outlook / iCloud 公开列表使用服务端快照，URL 仅携带短游标引用，绑定账号 generation、链接、收件人和平台规则；平台规则变更后旧游标失效。每个节点最多保留 64 个会话，每个快照最多 16 MiB、合计最多 64 MiB，30 分钟后到期；服务重启、会话回收或旧版本游标失效时，刷新列表即可重新开始。

首页和链接管理按每条具体取信链接统计访问次数，分别显示页面访问、JSON 调用、合计及最近访问时间；可以输入子邮箱、Token 或完整取信 URL 搜索。一次 GET 请求计一次（包括已过期或上游失败的已有链接），查询参数不另建统计项；未知链接、HEAD、后台查询与复制链接不计数。统计保存在当前节点的 SQLite 中，重启后保留，不生成配置同步事件；各子节点的访问次数暂不汇总到主节点。旧版本只记录了路由总数，无法还原历史每条链接的次数，新统计从升级后开始。链接有效期在创建后固定。

仅管理员可删除：`POST /api/admin/accounts/<id>/messages/delete`，请求 `{ "refs": ["服务签名的邮件引用"] }`。登录后需发送 Cookie 及 `X-CSRF-Token`，最多 100 封；返回逐封 `{ref,success,error}`。UI 只移除成功项，失败项保留可重试。引用绑定账号、来源协议、源文件夹及 ID，IMAP 额外绑定 UIDVALIDITY 和 UID。

Outlook 采用标准 Graph DELETE，通常将收件箱/垃圾箱邮件移入“已删除邮件”，需要 `Mail.ReadWrite` 授权。iCloud 使用 IMAP MOVE；未提供 MOVE 时仅在支持 UIDPLUS 的情况下使用 UID COPY + UID STORE + **UID EXPUNGE**，不使用全局 EXPUNGE/CLOSE。文件夹 UIDVALIDITY 变化则拒绝删除并要求刷新；已不存在的邮件按成功处理。来源为 Graph 的邮件不会被当成 IMAP UID 回退删除。不提供永久清空或公开删信 API。

删除后主节点立即清缓存，事务提交账号 generation 事件，子节点下次同步清缓存。同步前子节点存在最多一个轮询周期的可见延迟；缓存的 epoch 会拒绝删除前开始的旧请求重新填入过时数据。上游超时也会触发失效，用户应刷新核对后重试。

## 子节点

先在管理端创建节点，再在另一台主机运行相同程序/镜像：

```sh
NODE_ROLE=replica MASTER_URL=https://mail.example.com \
NODE_ID=... NODE_TOKEN=... DATA_DIR=./replica-data ./bin/mail-lite
```

容器使用 `docker compose -f compose.replica.yml --env-file .env.replica up -d --build`。每个子节点独立部署，配置不同凭据和数据卷。默认只接受主节点 HTTPS，localhost 测试允许 HTTP；受控私网可明确设置 `ALLOW_INSECURE_NODE_HTTP=true`。

节点卡片显示“连接 IP”，在成功同步心跳时自动记录主节点看到的连接来源地址，支持 IPv4 和 IPv6。节点停用或离线时保留最后一次记录，无需在子节点另行填写。

子节点宕机后，只要原凭据未被撤销，恢复服务器和子节点程序即可自动重连，无需修改配置。临时禁止接入可在主节点管理端点击“停用节点”，再次点击“启用节点”后按原来的轮询周期恢复同步和取信。后台启用只恢复接入权限，服务器和子节点程序仍需保持运行。停用节点收到主节点拒绝后立即停止取信；与主节点失联时沿用最长 120 秒的停止取信期限。

管理员接口 `PUT /api/admin/nodes/<id>/status` 接收 `{ "disabled": true }` 或 `{ "disabled": false }`；状态变更保留 ID、凭据和同步游标。`DELETE /api/admin/nodes/<id>` 永久撤销凭据；`DELETE /api/admin/nodes/<id>/record` 仅删除已撤销记录，删除后旧凭据仍无法接入。这些接口要求登录 Cookie 与 `X-CSRF-Token`。

初次同步使用固定一致性快照、1000 条一页；阶段数据和分页进度写入本地 `stage.db`，中断后继续。完整快照安装通过 SQLite 事务切换，随后按连续游标拉取 500 条一批的变更、删除和失效事件。数据与事件同一事务写入；事件保留 30 天，游标过旧会重新取快照。快照会话有效 15 分钟，主节点重启或会话过期后安全重新初始化。默认每 5 秒同步，初次追上后才可读取；连续 120 秒同步失败停止取信。`/health/live` 检查进程，`/health/ready` 未就绪时返回 503。

子节点持有只读管理配置及 iCloud IMAP 凭据（本地加密），不接收 Outlook Refresh Token、管理员密码或原始公开链接 token。Outlook Access Token 按需从主节点获取，短期内存缓存且只能申请读取权限。节点凭据撤销后下次联系主节点立即停用；网络隔离时最长 120 秒后停用。不提供主节点自动故障切换或长期离线取信。

## 并发与限制

每个节点默认最多 16 个上游操作（`UPSTREAM_CONCURRENCY=1..128`），同一账号串行；普通取信重复请求合并、8 秒短期缓存，最多 256 条、64 MiB。缓存请求不占用上游连接。请求 3 秒取不到上游许可返回繁忙。正文读取限制 2 MiB，单次导入请求限制 32 MiB，最多 20 个文件。管理端使用 20 封游标分页，不受旧扫描范围限制；Graph 跟随服务端 nextLink，IMAP 固定 UID 和接收时间，只按页读取头部。浏览器的邮件缓存仅在本次登录的内存中，退出登录后清除，不写入本地存储。

真实邮箱的延迟、OAuth 授权、IMAP 特性和服务商限流会影响冷缓存吞吐；本地模拟验收不替代真实账户压测。管理员需为 Outlook 应用授予读取、离线访问、必要的 IMAP 和删信权限。Outlook / iCloud 邮件仅在内存短期缓存，MX 邮件在主节点加密保存；没有附件下载、临时邮箱、转发配置、备份、皮肤包、桌面程序或浏览器扩展。

## 开发与验证

```sh
go test ./...
go vet ./...
cd web/ui && npm ci && npm run build
```

修改前端后必须重新 `npm run build` 并编译 Go。开发可 `npm run dev`，Vite 将 `/api` 代理到本机 8080。

完整规模验收（本地模拟，不访问真实邮箱）：

```sh
MAIL_LITE_ACCEPTANCE=1 go test ./internal/core -run '^TestScaleSyncAndLoad$' -v -timeout 20m -count=1
```

它通过真实导入代码生成 1000 主邮箱 / 100000 链接，验证服务器分页、10 万条导出及四个子节点完整同步，随后 200 并发读取持续 10 分钟，保留正常 8 秒缓存 TTL 和 5 秒轮询。阈值 P95 < 1 秒、错误率 < 1%。`MAIL_LITE_LOAD_DURATION=30s` 可快速检查，但不构成十分钟验收。常规测试覆盖凭据轮换、权限/CSRF、部分删除、精确 UID、安全 IMAP fallback、精确收件人与扫描边界、游标断档及节点撤销等。

浏览器验收：终端一运行 `MAIL_LITE_QA_ADDR=127.0.0.1:18080 go test ./internal/core -run '^TestBrowserFixture$' -v -count=1 -timeout 16m`，终端二在 `web/ui` 运行 `node browser-test.mjs`。测试进程 15 分钟后自动停止；模拟邮箱仅存在于 Go 测试中，不进入生产二进制。默认使用 Windows Edge，可用 `BROWSER_PATH` 指定 Chromium。覆盖 360/390/768/1280、三种主题、公共页面及手机核心操作，截图在 `.cache/screenshots`。

同一测试进程上运行 `node account-browser-test.mjs`，额外覆盖 360 / 390 / 768 / 1280 / 1920 与三种主题的邮箱列表和凭据抽屉、Token 复制 / 隐藏 / 关闭清理、iCloud 凭据、所选 TXT、筛选 CSV、无凭据 JSON、失败后重试及编辑保留凭据。

销售工作台独立验收：在 `mail-lite` 目录启动 `MAIL_LITE_QA_ADDR=127.0.0.1:18093 go test ./internal/core -run '^TestBrowserFixture$' -v -count=1 -timeout 20m`，再在 `web/ui` 运行 `node sales-browser-test.mjs`。PowerShell 使用 `$env:MAIL_LITE_QA_ADDR='127.0.0.1:18093'` 设置环境变量；脚本默认连接该端口，可用 `QA_BASE_URL` 覆盖。测试覆盖四种宽度、明暗主题、导入、映射、出库幂等重试、交付与退库，使用临时库存和虚构 SMTP 配置，并拦截检测启动请求；截图默认保存到系统临时目录下的 `mail-lite-sales-browser`。

自动关联专项浏览器验收：额外设置 `MAIL_LITE_QA_SOURCE_LINKS=1`、`MAIL_LITE_QA_ADDR=127.0.0.1:18094` 后启动同一测试，再运行 `node sales-source-links-browser-test.mjs`。使用独立临时数据库和模拟邮箱，覆盖补齐预览/提交/幂等、手动覆盖和恢复、打开取信页及内部检测，不发送 SMTP 邮件；截图保存在系统临时目录的 `mail-lite-source-links-browser`。

系统设置浏览器验收：设置 `MAIL_LITE_QA_ADDR=127.0.0.1:18095` 启动上述测试，在 `web/ui` 运行 `node settings-browser-test.mjs`。验证常规设置保存、桌面/手机菜单、四种宽度及明暗主题、密码校验、修改后退出所有会话及新旧密码登录；密码修改只作用于该测试的临时数据库。截图保存在系统临时目录的 `mail-lite-settings-browser`。


## 自有域名 MX 收信

新增的 **MX 域名收信** 类型直接接收 SMTP 邮件，不需要 Outlook Token 或 iCloud 密码。每个域名在邮箱管理中对应一个 `postmaster@域名` 管理账号，收件范围为该域名下的任意地址。子域名需单独添加；域名创建后不能改名或与其他类型互换。

### 启用收信入口

在主节点 `.env` 中配置并重启程序（修改源码后先重新构建）：

```dotenv
SMTP_ENABLED=true
SMTP_LISTEN_ADDR=:2525
SMTP_HOSTNAME=mx.example.com
# 可选：两项同时填写，提供 SMTP STARTTLS。
SMTP_TLS_CERT=
SMTP_TLS_KEY=
```

`SMTP_ENABLED` 默认 `false`。主节点会在启动时检查监听端口和证书；配置错误会使启动失败。`/health/ready` 会反映已启用的 SMTP 监听器是否正常运行。最多同时接入 32 个 SMTP 连接，每封最多 100 个收件人、原始邮件最多 10 MiB、单个正文最多 2 MiB；不接收超限内容。SMTP 仅接收已添加且启用的域名，不提供外域中继或发信服务。销售检测使用的发件池配置仍独立存在。

Docker 使用可选覆盖配置，容器继续以普通用户监听 2525，对外提供 TCP 25：

```sh
docker compose -f compose.yml -f compose.mx.yml up -d --build
```

证书路径必须在容器内可读；例如将证书放在已有持久卷的 `/data/certs/` 下，并填写相应容器路径。原生部署可直接监听 `:25`（需要相应操作系统权限），或将公网 TCP 25 转发至程序的 2525。公网邮件服务器通过 **25 端口** 投递；把程序改到其他端口不能替代公网 25 的开放。

### DNS 和使用流程

假设收信域名为 `example.com`，收信主机为 `mx.example.com`：

| 名称 | 类型 | 值 | 优先级 |
| --- | --- | --- | --- |
| `mx.example.com` | A | 收信服务器公网 IPv4 | — |
| `example.com` | MX | `mx.example.com` | 10 |

只有在 IPv6 入站投递也可用时才添加 AAAA。收信主机采用纯 DNS 解析，不使用 HTTP/CDN 代理。MX 必须指向主机名，不能填写 IP、URL 或端口。DNS 配置不会自动修改，由管理员在域名服务商处填写。

1. 在“邮箱 → 添加邮箱”选择“MX 域名收信”，填写域名和保留天数；默认 30 天，`0` 为永久保存，允许范围 0–36500。
2. 保存后，在“邮箱详情”查看 DNS 配置、SMTP 运行状态、邮件数和最近收信时间。
3. 无需先创建收件地址；向 `任意名称@example.com` 投递的邮件均可保存。链接管理中选中该 MX 主邮箱，批量导入同域名地址，生成独立取件链接。
4. 取件链接可在收信前或收信后创建，读取保留期内的邮件。每条链接继续独立绑定收件地址、平台和有效期；收信本身不会创建公开链接。
5. MX 来源可按原流程映射到销售库存，沿用来源关联、交付链接和检测。停用域名会拒绝新投递并禁止取件，删除域名会清理邮件及链接。

取件沿用现有 API。例如使用后台提供的 JSON 地址：

```text
GET /api/mailboxes/p0/<主邮箱随机标识>/alice/messages?limit=1&details=1
```

响应继续包含 `recipient`、`items`、`order`、`has_more`、`next_cursor`。`details=1` 返回正文；默认仅返回摘要；通过返回的 `ref` 可读取单封邮件。分页最多 20 封，并固定首次查询边界，收到新邮件时需要刷新首屏。API 地址本身承载取件权限，应按原链接规则保管。修改查询参数不能切换收件人。

实际收件人以 SMTP 投递信封为准，支持密送、多个收件人和加号地址。伪造 `To`、`Delivered-To` 等邮件头不会让邮件出现在其他地址的链接中。首版统一放入收件箱，垃圾箱为空；正文继续清理 HTML 并隔离展示。附件随加密原文保留，暂不提供下载。

导入导出格式：

```text
postmaster@example.com----MX----30
```

### 存储、子节点与验收

邮件在主节点独立的 `data/mx-mail.db` 中保存，原文和正文用现有 `data.key` 加密；SMTP 仅在持久化成功后确认接收，存储故障返回临时错误，让发件方重试。过期邮件立即停止返回，启动时和每小时分批清理；MX 邮件的管理员删除操作直接删除本地数据。异常中断遗留的孤立邮件在启动或维护时清理。

停机备份和恢复时一起保留 `mail-lite.db`、`mx-mail.db` 和 `data.key`。运行中的 SQLite 数据库应使用一致性备份方式，不能只复制主数据库文件而漏掉 WAL。

子节点同步域名和链接配置，通过现有节点凭据向主节点按需读取邮件；不在子节点保存邮件正文。主节点断连或节点凭据撤销时无法继续取件，并返回暂不可用。主节点和子节点需一并升级到支持 MX 的版本。

本地自动验证：`go test ./internal/core -run '^TestMX' -count=1`。浏览器验收使用下述独立临时实例，不操作真实数据：

```powershell
$env:MAIL_LITE_QA_MX_ADDR='127.0.0.1:18096'
go test ./internal/core -run '^TestMXBrowserFixture$' -v -count=1 -timeout 10m
# 另一个终端，在 web/ui 目录运行：
node mx-browser-test.mjs
```

公网入站 TCP 25 尚需部署时确认。上线验收应检查安全组、防火墙和网络提供商限制，再验证 DNS 生效，从外部邮箱发信，并通过公开 API 读取对应邮件。本地模拟通过不能替代公网收信验收。
