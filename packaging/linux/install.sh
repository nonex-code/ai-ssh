#!/bin/sh
# Install the application icon and launcher for the current user. It works
# from the repository (packaging/linux/install.sh) and from the release archive
# (install-icons.sh, next to ai-ssh.png and ai-ssh.desktop).
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ -f "$here/ai-ssh.png" ] && [ -f "$here/ai-ssh.desktop" ]; then
  icon="$here/ai-ssh.png"
  desktop="$here/ai-ssh.desktop"
else
  root=$(CDPATH= cd -- "$here/../.." && pwd)
  icon="$root/internal/gui/assets/logo-512.png"
  desktop="$root/packaging/linux/ai-ssh.desktop"
fi

data=${XDG_DATA_HOME:-$HOME/.local/share}
mkdir -p "$data/icons/hicolor/512x512/apps" "$data/applications"
cp "$icon" "$data/icons/hicolor/512x512/apps/ai-ssh.png"
cp "$desktop" "$data/applications/ai-ssh.desktop"
command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$data/applications" || true
echo "installed ai-ssh.png and ai-ssh.desktop in $data"
