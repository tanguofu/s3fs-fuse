#!/bin/bash 

fmt_info(){
  printf '%s info: %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*" 
}


# check mountpoint is already mounted
info=$(df -h "$MOUNT_PATH" 2>&1)
if [[ "$info" =~ "No such" ]]; then
  mkdir -p "$MOUNT_PATH"
  fmt_info "mkdir -p $MOUNT_PATH"
elif [[ "$info" =~ "not connected" ]]; then
  fusermount -u "$MOUNT_PATH"
  fmt_info "$MOUNT_PATH is not connected: $info"
elif [[ "$info" =~ "cosfs-mount" ]]; then 
  fmt_info "$MOUNT_PATH  is mounted: $info"
fi


set -e
COS_OPTIONS="$COS_OPTIONS -oallow_other -ononempty -ocompat_dir -odbglevel=warn"


if [ -n "$USE_MEM_CACHE" ]; then
  # calc min(2GB, Mem/4)
  min_memory_mb=$(grep MemTotal /proc/meminfo | awk '{printf("%.0f", $2 / 1024 / 4)}' | awk '{print ($1 < 2048) ? $1 : 2048}')
  mkdir -p /cos_tmpfs && mount -t tmpfs -o size="${min_memory_mb}"M tmpfs /cos_tmpfs
  COS_OPTIONS="$COS_OPTIONS -ouse_cache=/cos_tmpfs -odel_cache -oensure_diskfree=64"
else
  # tmp is shared by all container of pod so use container name to isolation
  CACAHE_DIR="/tmp/${CONTAINER_NAME:-cosfs}"
  mkdir -p "$CACAHE_DIR"
  # COS_OPTIONS="$COS_OPTIONS -ouse_cache=$CACAHE_DIR -odel_cache -oensure_diskfree=2048"
  COS_OPTIONS="$COS_OPTIONS -oensure_diskfree=8192"
fi

if [ -z "$PARALLEL_COUNT" ]; then
COS_OPTIONS="$COS_OPTIONS -oparallel_count=16 -omultireq_max=16"
else
COS_OPTIONS="$COS_OPTIONS -oparallel_count=$PARALLEL_COUNT -omultireq_max=$PARALLEL_COUNT"
fi

if [ -z "$MULTIPART_SIZE" ]; then
COS_OPTIONS="$COS_OPTIONS -omultipart_size=16"
else
COS_OPTIONS="$COS_OPTIONS -omultipart_size=$MULTIPART_SIZE"
fi

restartPolicy=${RESTART_POLICY:-Always}

if [[ "${restartPolicy}" =~ "Always" ]]; then 
  fmt_info "restartPolicy:$restartPolicy do not check sidecar status"
else
  fmt_info "restartPolicy:$restartPolicy start check sidecar status"
  /cosfs_watcher.sh &
fi

set -x
# strip \n of the url
QCLOUD_TMS_CREDENTIALS_URL=$(echo -n "$QCLOUD_TMS_CREDENTIALS_URL" | tr -d '\n' | tr -d '\r' | tr -d ' ')

if [ -z "$QCLOUD_TMS_CREDENTIALS_URL" ]; then 
  /cosfs-mount "$BUCKET" -f "$MOUNT_PATH" -ourl="$COS_URL" -opasswd_file="$PASSWD_FILE" "$COS_OPTIONS"
else
  /cosfs-mount "$BUCKET" -f "$MOUNT_PATH" -ourl="$COS_URL" -osts_agent_url="$QCLOUD_TMS_CREDENTIALS_URL" "$COS_OPTIONS"
fi
ret=$?

set +x
set +e

fmt_info "cosfs-mount exit $ret"

# clear mount point
info=$(df -h "$MOUNT_PATH" 2>&1)
if [[ "$info" =~ "not connected" ]]; then
  fusermount -u "$MOUNT_PATH"
  fmt_info "$MOUNT_PATH is not connected: $info"
fi

# keep exit code 0 
exit 0