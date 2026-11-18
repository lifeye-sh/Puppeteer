#!/bin/bash
set -euo pipefail
# All mutations below are inside a disposable EL7 test container.
rpm -i --nodeps --noscripts /fixtures/containerd.io-1.6.33-3.1.el7.x86_64.rpm
yum --disablerepo='*' --enablerepo=vault-base -y install python3
mv /opt/puppeteer-compat/libseccomp-2.6.1 /tmp/prebuilt-library
mkdir -p /test-bin
cat > /test-bin/rpm <<'SH'
#!/bin/bash
if [[ $* == '-q libseccomp containerd.io docker-ce' ]]; then
 /usr/bin/rpm -q libseccomp containerd.io
 echo docker-ce-26.1.4-1.el7.x86_64
else
 exec /usr/bin/rpm "$@"
fi
SH
cat > /test-bin/docker <<'SH'
#!/bin/bash
case "$1" in
 version) echo 26.1.4;;
 context) echo unix:///var/run/docker.sock;;
 image) exit 0;;
 run) [[ $* == *'--network none'* && $* == *'--user 70:70'* && $* == *'/probe/data'* ]] || exit 2; [[ ! -e /tmp/probe-fail ]];;
 *) exit 2;;
esac
SH
cat > /test-bin/curl <<'SH'
#!/bin/bash
while [[ $# -gt 0 ]]; do
 if [[ $1 == -o ]]; then cp /fixtures/puppeteer-el7-libseccomp-2.6.1-amd64.tar.gz "$2"; exit; fi
 shift
done
exit 2
SH
chmod 755 /test-bin/*
export PATH=/test-bin:$PATH
helper=/fixtures/repair-el7-libseccomp.sh
runc --version | grep 'libseccomp: 2.3.1'
bash "$helper" --check
test ! -e /etc/ld.so.conf.d/00-puppeteer-libseccomp.conf
bash "$helper" --apply
runc --version | grep 'libseccomp: 2.6.1'
ldd /usr/bin/runc | grep '/opt/puppeteer-compat/libseccomp-2.6.1/lib/libseccomp.so.2'
rpm -V libseccomp
bash "$helper" --apply
bash "$helper" --rollback
runc --version | grep 'libseccomp: 2.3.1'
touch /tmp/probe-fail
if bash "$helper" --apply; then echo 'Failed probe accepted'; exit 1; fi
test ! -e /etc/ld.so.conf.d/00-puppeteer-libseccomp.conf
runc --version | grep 'libseccomp: 2.3.1'
rpm -V libseccomp
echo 'PASS: ABI activation, repeat apply, rollback, failed-probe rollback, RPM files unchanged. Docker CLI is mocked in this test.'
