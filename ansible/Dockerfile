# Ansible corre dentro de este contenedor y no directo en la Mac: en macOS,
# los procesos que Ansible crea con fork() truenan al usar la red si el
# proceso principal ya la usó ("A worker was found in a dead state").
# En Linux no pasa. Ver ansible/README.md.
FROM python:3.13-slim

# El plugin de SSM de AWS, en la versión que corresponde al procesador
# (arm64 en la Mac, amd64 en una compu con Intel).
ARG TARGETARCH
RUN apt-get update \
 && apt-get install -y --no-install-recommends curl ca-certificates \
 && case "$TARGETARCH" in arm64) SMP=ubuntu_arm64 ;; *) SMP=ubuntu_64bit ;; esac \
 && curl -fsSL "https://s3.amazonaws.com/session-manager-downloads/plugin/latest/$SMP/session-manager-plugin.deb" -o /tmp/smp.deb \
 && dpkg -i /tmp/smp.deb \
 && rm /tmp/smp.deb \
 && apt-get purge -y curl && apt-get autoremove -y \
 && rm -rf /var/lib/apt/lists/*

# Ansible y lo que necesitan sus plugins de AWS y de certificados.
RUN pip install --no-cache-dir \
      ansible-core==2.21.5 boto3==1.43.108 botocore==1.43.108 cryptography==50.0.2

COPY requirements.yml /tmp/requirements.yml
RUN ansible-galaxy collection install -r /tmp/requirements.yml -p /usr/share/ansible/collections

# Aquí se monta la carpeta ansible/ del repo, para que lea ansible.cfg.
WORKDIR /ansible
