#!/bin/bash

export LLGOROOT=$(pwd)

# llgo run subdirectories under _demo and _pydemo
total=0
failed=0
failed_cases=""
for d in ./_demo/* ./_pydemo/*; do
  total=$((total+1))
  if [ -d "$d" ]; then
    echo "Testing $d"
    llgo run -v $d
    if [ $? -ne 0 ]; then
      echo "FAIL"
      failed=$((failed+1))
      failed_cases="$failed_cases\n$d"
    else
      echo "PASS"
    fi
  fi
done

echo "=== Done"
echo "$((total-failed))/$total tests passed"
if [ "$failed" -ne 0 ]; then
  echo -e "Failed cases:$failed_cases"
  exit 1
fi
