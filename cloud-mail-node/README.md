# Cloud Mail 只读节点部署

为 [Cloud Mail](https://github.com/lsh-okok/cloud-mail) 部署独立的只读节点，自动导入应用镜像、保存接入配置、创建容器和数据卷，并等待首次同步。

## 安装节点

先准备以下文件，并放到目标 Linux 服务器：

```text
node.env                         # 从主节点新建节点的结果下载，每台服务器单独创建
node-image-release/
  node-image.tar.gz              # 同版本、同 CPU 架构的 Cloud Mail 应用镜像
  node-image.json                # 镜像 ID 与压缩包 SHA-256
```

下载并执行安装脚本：

```sh
curl -fsSL https://raw.githubusercontent.com/lsh-okok/ok-email-scripts/main/cloud-mail-node/install-node.sh -o install-node.sh && sudo sh install-node.sh node.env ./node-image-release 13001
```

`13001` 是示例访问端口，请选择空闲端口；省略最后一项时默认使用 `3001`。节点容器必须能够访问 `node.env` 中的主节点地址。HTTPS 可直接使用；受信局域网使用 HTTP 时，须在生成节点配置时明确允许 HTTP，配置包含 `NODE_ALLOW_INSECURE_HTTP=true`。

安装脚本支持 Linux，已有 Docker、coreutils 和 diffutils 时直接使用本地镜像包，不访问镜像源或 npm。若缺少 Docker 或校验工具，会尝试通过支持的 apt/dnf 发行版的软件源安装，需要 root/sudo 权限及可用的软件源。无法访问 GitHub 的目标服务器可以在其他电脑下载脚本后复制过去，再执行：

```sh
sudo sh install-node.sh node.env ./node-image-release 13001
```

## 制作镜像发布包

在已有对应 Cloud Mail 应用镜像的 Docker 主机执行：

```sh
curl -fsSL https://raw.githubusercontent.com/lsh-okok/ok-email-scripts/main/cloud-mail-node/export-node-image.sh -o export-node-image.sh
sudo sh export-node-image.sh cloud-mail-app:release /srv/node-image-release
```

将 `cloud-mail-app:release` 替换为实际镜像标签或 ID。镜像应由 Cloud Mail 项目的 Dockerfile 构建，与主节点代码版本及目标服务器 CPU 架构匹配。输出目录的父目录必须已存在，输出目录本身必须是新目录。导出主机需要 Docker、gzip、sha256sum、mktemp 和 mkfifo。

脚本生成 `node-image.tar.gz` 和 `node-image.json`，目录权限为 `700`、文件为 `600`。将整个目录通过自己的文件传输方式复制到目标服务器，确保执行安装脚本的用户可读取。

## 重复执行与配置

相同的节点配置、端口和镜像版本可以重复执行，会启动已有容器并保留数据。遇到不同配置、端口或镜像版本时会停止并提示；此脚本不执行版本升级。

- 容器：`cloud-mail-node-<节点ID>`。
- 数据卷：`cloud-mail-node-<节点ID>-data`。
- 接入配置：执行用户的 `~/.cloud-mail/nodes/<节点ID>/node.env`，权限 `600`；使用 `sudo sh` 时通常位于 `/root/.cloud-mail/nodes/`。
- 首次同步等待上限为 180 秒；超时保留容器、配置和数据，可用 `sudo docker logs cloud-mail-node-<节点ID>` 查看原因。

`node.env` 含节点接入凭据及密钥，请保存在服务器上，不要提交到 GitHub。脚本本身不包含接入凭据，安装仍需主节点为该服务器创建有效的节点记录。
