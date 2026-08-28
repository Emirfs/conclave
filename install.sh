#!/usr/bin/env sh
# Conclave'in daemon'unu ve komutunu macOS veya Linux'a kurar.
#
#   curl -fsSL https://raw.githubusercontent.com/Emirfs/conclave/main/install.sh | sh
#
# Belirli bir surum veya baska bir dizin icin:
#
#   curl -fsSL .../install.sh | CONCLAVE_VERSION=v0.2.0 CONCLAVE_BIN=~/bin sh
#
# Masaustu istemcisi bu platformlarda henuz yayinlanmiyor; kurulan sey yerel
# API'yi tasiyan daemon ile onun istemcisi olan `conclave` komutu.

set -eu

REPO="Emirfs/conclave"
VERSION="${CONCLAVE_VERSION:-latest}"
BIN_DIR="${CONCLAVE_BIN:-$HOME/.local/bin}"

step() { printf '  %s\n' "$1"; }
fail() { printf 'conclave: %s\n' "$1" >&2; exit 1; }

case "$(uname -s)" in
  Linux) goos=linux ;;
  Darwin) goos=darwin ;;
  *) fail "desteklenmeyen isletim sistemi: $(uname -s)" ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) goarch=amd64 ;;
  arm64 | aarch64) goarch=arm64 ;;
  *) fail "desteklenmeyen mimari: $(uname -m)" ;;
esac

for tool in curl tar; do
  command -v "$tool" >/dev/null 2>&1 || fail "$tool gerekli ama bulunamadi"
done

asset="conclave-$goos-$goarch.tar.gz"
if [ "$VERSION" = "latest" ]; then
  base="https://github.com/$REPO/releases/latest/download"
else
  base="https://github.com/$REPO/releases/download/$VERSION"
fi

printf '\nConclave\n\n'
step "platform: $goos/$goarch"
step "surum:    $VERSION"
step "dizin:    $BIN_DIR"

work="$(mktemp -d)"
# Bir hata durumunda yarim indirilen dosyalar ortada kalmasin.
trap 'rm -rf "$work"' EXIT INT TERM

step "indiriliyor: $asset"
curl -fsSL "$base/$asset" -o "$work/$asset" ||
  fail "indirilemedi: $base/$asset"

# Yayin checksums-unix.txt tasiyor. Dogrulama zorunlu degil: dosya yoksa ya da
# makinede sha256 araci yoksa kurulum devam eder, ama sessizce degil.
if curl -fsSL "$base/checksums-unix.txt" -o "$work/checksums.txt" 2>/dev/null; then
  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$work/$asset" | cut -d' ' -f1 | tr 'a-f' 'A-F')"
  elif command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$work/$asset" | cut -d' ' -f1 | tr 'a-f' 'A-F')"
  else
    actual=""
    step "uyari: sha256 araci yok, dogrulama atlandi"
  fi
  if [ -n "$actual" ]; then
    expected="$(grep " $asset\$" "$work/checksums.txt" | cut -d' ' -f1 | tr 'a-f' 'A-F' || true)"
    if [ -z "$expected" ]; then
      step "uyari: $asset icin checksum kaydi yok"
    elif [ "$expected" != "$actual" ]; then
      fail "SHA256 tutmuyor; indirilen dosyaya guvenilmez"
    else
      step "sha256 dogrulandi"
    fi
  fi
else
  step "uyari: checksums-unix.txt alinamadi, dogrulama atlandi"
fi

tar -xzf "$work/$asset" -C "$work"
[ -f "$work/conclave" ] || fail "arsivde conclave calistirilabiliri yok"

mkdir -p "$BIN_DIR"
# Calisan bir daemon'un uzerine yazmak "text file busy" verir; once tasiyip
# sonra yerine koymak bunu asar.
if [ -e "$BIN_DIR/conclave" ]; then
  mv "$BIN_DIR/conclave" "$BIN_DIR/conclave.eski" 2>/dev/null || true
  rm -f "$BIN_DIR/conclave.eski" 2>/dev/null || true
fi
install -m 0755 "$work/conclave" "$BIN_DIR/conclave"
step "kuruldu: $BIN_DIR/conclave"

printf '\n%s\n' "$("$BIN_DIR/conclave" version 2>/dev/null || echo 'surum okunamadi')"

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *)
    printf '\n%s\n' "PATH'te degil. Kabuk yapilandirmana ekle:"
    printf '  %s\n' "export PATH=\"$BIN_DIR:\$PATH\""
    ;;
esac

printf '\n%s\n' 'Baslatmak icin:'
printf '  %s\n\n' 'conclave daemon'
