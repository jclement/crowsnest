# shellcheck shell=sh
# /etc/profile.d/crowsnest-devbox.sh, installed by deploy/Dockerfile.
#
# Debian's /etc/profile resets PATH for login shells, which drops the mise shims the image's
# ENV put first. herdr panes and `docker exec -it … bash -l` can be login shells, and the
# orchestrator's `claude` must resolve in them, so put the shims and ~/.local/bin back in front.
crowsnest_shims="${MISE_DATA_DIR:-$HOME/.local/share/mise}/shims"
case ":$PATH:" in
  *":$crowsnest_shims:"*) ;;
  *) PATH="$crowsnest_shims:$HOME/.local/bin:$PATH" ;;
esac
unset crowsnest_shims
export PATH
