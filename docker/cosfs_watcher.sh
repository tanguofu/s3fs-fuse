#!/bin/bash 

fmt_error() {
  printf '%s error: %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*" >&2
}

fmt_info(){
  printf '%s info:  %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*" 
}

# checkout
is_other_container_start="0"

for i in {1..6}; do
  is_other_container_start=$(/sidecar check |grep -c "found one container in pod")

  if [ "$is_other_container_start" -eq 0 ]; then 
    fmt_info "sleep 10s to wait other container start at $i times"
    sleep 10s    
  fi

  fmt_info "found $is_other_container_start container in pod: $POD_NAMESPACE/$POD_NAME, wait them exit"
  break
done


restartPolicy=${RESTART_POLICY:-Always}
# wait 
while true
do

/sidecar wait; ret=$?

if [ "$ret" -eq 0 ] || [ "$restartPolicy" == "Never" ]; then
  fmt_info "restartPolicy is $restartPolicy and exitcode is $ret  kill cosfs and exit"
  kill -s SIGTERM $(pgrep "cosfs-mount")
  exit 0
fi

done




