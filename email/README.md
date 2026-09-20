# Outlook Email production scripts

These scripts install a primary-and-replica email deployment. They contain no passwords, IP addresses, tokens, image names, or application data.

Download the deployment script on each Ubuntu host:

```bash
curl -fsSL https://raw.githubusercontent.com/lsh-okok/ok-email-scripts/main/email/deploy-production.sh \
  -o /tmp/deploy-production.sh
sudo bash /tmp/deploy-production.sh --help
```

Set this to the immutable image digest published by your private registry:

```text
ghcr.io/YOUR_GITHUB_OWNER/email@sha256:YOUR_IMAGE_DIGEST
```

On the primary host, replace the example domains and private replica addresses:

```bash
sudo bash /tmp/deploy-production.sh primary \
  --image 'ghcr.io/YOUR_GITHUB_OWNER/email@sha256:YOUR_IMAGE_DIGEST' \
  --registry-user 'YOUR_GITHUB_OWNER' \
  --admin-domain 'admin.example.com' \
  --query-domain 'mail.example.com' \
  --acme-email 'ops@example.com' \
  --replica '10.66.0.12' \
  --replica '10.66.0.13'
```

The script installs Docker when needed and interactively requests a GitHub classic `read:packages` token, then the administrator password. It logs out of GHCR after the image pull.

Create each replica node in the primary web administration interface, then run this on the matching replica:

```bash
sudo bash /tmp/deploy-production.sh replica \
  --image 'ghcr.io/YOUR_GITHUB_OWNER/email@sha256:YOUR_IMAGE_DIGEST' \
  --registry-user 'YOUR_GITHUB_OWNER' \
  --master 'https://admin.example.com' \
  --node-id 'NODE_ID_FROM_PRIMARY' \
  --fingerprint 'PRIMARY_FINGERPRINT' \
  --bind-ip '10.66.0.12'
```

The replica script requests the GHCR token and then the matching one-time enrollment token. The replica `5000` port must be reachable only from the primary gateway over a private network or VPN.

Install the primary backup command with:

```bash
curl -fsSL https://raw.githubusercontent.com/lsh-okok/ok-email-scripts/main/email/backup-primary.sh \
  -o /tmp/backup-primary.sh
sudo install -m 700 /tmp/backup-primary.sh /usr/local/sbin/backup-primary.sh
sudo /usr/local/sbin/backup-primary.sh
```
