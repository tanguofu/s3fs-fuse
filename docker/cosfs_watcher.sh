#!/bin/bash 

fmt_error() {
  printf '%s error: %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*" >&2
}

fmt_info(){
  printf '%s info:  %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*" 
}

# checkout
is_other_container_start="0"

# wait in 30 min
for i in {1..30}; do
  is_other_container_start=$(/sidecar check  2>&1 |grep -c "found one container in pod")

  if [ "$is_other_container_start" -gt 0 ]; then 
     fmt_info "found $is_other_container_start container in pod: $POD_NAMESPACE/$POD_NAME, wait them exit"
     break
  fi
  
  fmt_info "try $i/30 times sleep to wait other container started"
  sleep 60s 
done


restartPolicy=${RESTART_POLICY:-Always}
# wait 
while true
do

/sidecar wait; ret=$?

if [ "$ret" -eq 0 ] || [ "$restartPolicy" == "Never" ]; then
  fmt_info "restartPolicy is $restartPolicy and exitcode is $ret kill cosfs and exit"
  kill -s SIGTERM $(pgrep "cosfs-mount")
  exit 0
fi

done




