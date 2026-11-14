# Cloud Mail 节点一键安装

所有节点共用本目录的 `install-node.sh`。在 Cloud Mail 的「节点管理 → 新增节点」中填写名称，页面会自动生成一条带当前节点参数的安装命令，复制到目标 Linux 服务器执行即可。

通用脚本固定地址：

```text
https://raw.githubusercontent.com/lsh-okok/ok-email-scripts/main/cloud-mail-node/install-node.sh
```

无需为每台服务器修改脚本，也无需提前准备 `node.env` 或镜像文件。脚本根据安装参数向主节点领取独立配置和安装包，校验后自动启动节点并等待同步。

## 使用方式

推荐直接复制节点管理生成的一键命令：它会先从 GitHub 完整下载通用脚本，再附带 `--primary-url`、`--install-token`、`--port` 执行。主地址使用受信局域网 HTTP 时，命令额外包含 `--allow-insecure-http`。

如果已经下载了通用脚本，在新增结果中切换到「已下载安装脚本」，复制该页面提供的带参数命令。其形式如下，实际参数以节点管理生成的值为准：

```sh
sudo sh install-node.sh --primary-url 'https://mail.example.com' --install-token 'INSTALL_TOKEN_FROM_NODE_MANAGEMENT' --port 3001
```

查看参数帮助：

```sh
sh install-node.sh --help
```

目标服务器需有 curl 或 wget，并能访问主节点；直接使用一键命令时还需能访问 GitHub。缺少 Docker 或工具时，脚本会尝试通过支持的 apt/dnf 发行版安装，需要 root/sudo 权限及可用的软件源。源码构建模式还需要访问 Docker 镜像源和 npm；主节点已提供预构建镜像时只需从主节点下载并导入镜像。

## 安装参数与有效期

`--install-token` 是主节点生成的短期安装令牌，不是长期节点接入凭据。令牌默认有效一小时；主节点重启、节点停用/撤销或安装包更新会提前失效。请在有效期内完成配置及安装包下载。已经安装的节点继续使用自己的长期配置，不受安装令牌过期影响。

GitHub 通用脚本本身没有节点凭据和到期时间，可以长期保存、供多个节点复用。每个新增节点应使用页面为它生成的独立参数。主节点须更新至支持 `/api/node-install/:token/config.env` 的版本。

## 预构建镜像（可选）

目标服务器无法访问镜像源或 npm 时，在已有同版本、同 CPU 架构 Cloud Mail 应用镜像的 Docker 主机执行：

```sh
sudo sh export-node-image.sh cloud-mail-app:release /srv/node-image-release
```

将镜像标签替换为实际值，输出目录的父目录必须已存在，输出目录本身须为新目录。导出需要 Docker、gzip、sha256sum、mktemp 和 mkfifo，生成 `node-image.tar.gz` 和 `node-image.json`，记录镜像 ID 及压缩包 SHA-256。

将发布目录以只读方式挂载到主节点应用容器，并通过 `NODE_DEPLOYMENT_IMAGE_DIR` 指向容器内目录；应用进程须能读取这些文件。后续新增节点时，主节点自动向通用安装脚本提供镜像包，用户无需在节点服务器手动搬运镜像。完整配置见 Cloud Mail 项目的部署文档。

## 配置与重复执行

- 容器：`cloud-mail-node-<节点ID>`。
- 数据卷：`cloud-mail-node-<节点ID>-data`。
- 配置：执行用户的 `~/.cloud-mail/nodes/<节点ID>/node.env`，权限 `600`。使用 `sudo sh` 时通常位于 `/root/.cloud-mail/nodes/`。
- 同步等待上限为 180 秒；超时保留容器、配置和数据，可执行 `sudo docker logs cloud-mail-node-<节点ID>` 排查。

在安装参数有效期内重复执行相同命令，会保留原配置和数据。端口、配置或镜像冲突时停止并提示，脚本不执行升级。下载的配置不会作为 Shell 代码执行；安装包须通过 SHA-256 校验，预构建模式还检查镜像 ID。

安装参数和服务器上的 `node.env` 均应妥善保存，不要提交到 GitHub。
