#!/usr/bin/env bash
set -u
echo "=== [1/5] listening TCP ==="
ss -tulpn 2>/dev/null | grep -i LISTEN | head -40
echo
echo "=== [2/5] processes with php in maps ==="
PHP_MAPS=$(grep -l -i "php" /proc/[0-9]*/maps 2>/dev/null | cut -d/ -f3 | sort -u)
if [ -n "$PHP_MAPS" ]; then
  for pid in $PHP_MAPS; do
    [ -d "/proc/$pid" ] || continue
    echo "PID $pid exe=$(readlink -f /proc/$pid/exe 2>/dev/null) cmd=$(tr '\0' ' ' < /proc/$pid/cmdline 2>/dev/null | cut -c1-160)"
    grep -i php /proc/$pid/maps 2>/dev/null | awk '{print "   lib: "$6}' | sort -u | head -3
  done
else
  echo "  (none)"
fi
echo
echo "=== [3/5] other runtimes ==="
for rt in podman crictl lxc-ls nerdctl snap; do command -v $rt >/dev/null 2>&1 && echo "  found: $rt"; done
echo "  (done)"
echo
echo "=== [4/5] filesystem search for php binaries ==="
find / -xdev -path /proc -prune -o -path /sys -prune -o -path /dev -prune -o \( -name "php" -o -name "php[0-9]*" -o -name "php-fpm*" -o -name "libphp*.so" -o -name "lsphp*" \) -print 2>/dev/null | head -30
echo "  --- chevereto/env.php ---"
find / -xdev -path /proc -prune -o \( -name "chevereto*" -o -name "env.php" \) -print 2>/dev/null | grep -v /usr/src | head -15
echo
echo "=== [5/5] local web + reverse proxy config ==="
curl -sI -m 3 http://127.0.0.1 2>/dev/null | head -5 || echo "  no http on :80"
grep -rnE "proxy_pass|fastcgi_pass" /etc/nginx /etc/apache2 /etc/httpd /usr/local/nginx /www 2>/dev/null | head -10 || echo "  no nginx/apache configs"