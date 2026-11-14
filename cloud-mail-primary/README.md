# Cloud Mail 主节点一键安装

在新的 Linux 服务器执行一条命令，下载脚本并安装主节点：

```sh
curl -fsSL https://raw.githubusercontent.com/lsh-okok/ok-email-scripts/main/cloud-mail-primary/install-primary.sh -o install-primary.sh && bash install-primary.sh
```

脚本自动安装缺少的 Docker 与工具，下载并校验配套的服务器源码包，构建应用镜像，生成独立管理员密码、JWT 密钥和数据加密密钥，创建持久数据卷并启动服务。服务器无需单独安装 Node.js 或 pnpm。首次构建需要访问 GitHub、Docker 镜像源、系统软件源和 npm。

默认安装目录为 `/opt/cloud-mail-primary`，容器名为 `cloud-mail-primary`，网页端口为 `3000`，邮箱域为 `cloudmail.test`。脚本检测服务器 IPv4，安装成功后打印访问地址、管理员邮箱、初始密码和配置位置。检测到的地址不适合访问时，用 `--host` 指定服务器 IP 或域名。

## 自定义配置

例如在另一个空闲端口和独立目录部署：

```sh
sudo bash install-primary.sh --host 203.0.113.10 --port 13000 --dir /opt/cloud-mail-primary-new --name cloud-mail-primary-new
```

使用自己的邮箱域：

```sh
sudo bash install-primary.sh --host 203.0.113.10 --mail-domain example.com --admin-email admin@example.com
```

`--app-url https://mail.example.com` 可指定主站地址；HTTPS 仍需配置反向代理和证书。管理员邮箱必须属于指定的邮箱域。使用 `bash install-primary.sh --help` 查看完整参数。

## 安装范围

此脚本安装**管理主节点**，提供管理界面、主邮箱来源、收件链接、验证任务和只读节点管理。安装后在「节点管理 → 新增节点」生成子节点的一键安装命令。

此安装不启动 Postfix/OpenDKIM，**MX 收发未启用**；网页正常和主节点健康不代表公网收发邮件已经配置。完整 MX 服务还需要邮件容器、域名 DNS、TLS 证书及 TCP 25 配置，见 Cloud Mail 项目的服务器部署文档。

## 保留配置与数据

安装目录权限为 `700`，配置权限为 `600`。请妥善保管配置与持久卷备份，管理员密码只在首次成功安装时交付；不会上传到 GitHub。

对相同安装重复执行会校验已有部署并启动原容器，保留密钥和数据。遇到同名非本脚本容器、已有其他数据卷、端口或配置冲突时会停止并提示；脚本不自动接管或升级旧服务。不同实例请使用不同的 `--dir`、`--name` 和 `--port`。

已有本版本应用镜像时可用 `--image 镜像标签`；已有完整服务器版源码时可用 `--source-dir /absolute/source`，以省去默认源码包下载。镜像须符合 Cloud Mail Dockerfile 的运行路径和 Node.js 版本。

## 配套源码包

`source-manifest.json` 记录本次服务器源码包的文件名和 SHA-256。安装脚本固定其版本与摘要，校验成功后才解包构建。发布包仅包含构建所需源码白名单及 MIT 许可证，不含运行环境配置、数据库、证书、私钥或节点凭据。

原项目来源：[lsh-okok/cloud-mail](https://github.com/lsh-okok/cloud-mail)。MIT 版权声明与许可全文见 [LICENSE](LICENSE)，第三方资源许可随源码包保留。
