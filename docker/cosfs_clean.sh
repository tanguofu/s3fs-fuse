#!/bin/bash 

fmt_warn() {
  printf '%s warn: %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"
}

fmt_info(){
  printf '%s info: %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*" 
}

BASE=/cosfs/infer-2473243331

for dir in $(ls $BASE); do

  MOUNT_PATH="$BASE/$dir/cos"
  is_cosfs_mount=$(df -h "$MOUNT_PATH" 2>&1)
  if [[  "$is_cosfs_mount" =~ "cosfs-mount" ]]; then 
    fmt_info "$MOUNT_PATH cosfs is mounted: $is_cosfs_mount"
    continue
  fi


  if [[ "$is_cosfs_mount" =~ "not connected" ]]; then
    fusermount -u "$MOUNT_PATH"
    fmt_info "$MOUNT_PATH is not connected: $is_cosfs_mount"
  fi

  fmt_info "clean $BASE/$dir"
  rm -fr $BASE/$dir

done

mount |grep cosfs