#!/usr/bin/env python3
#
# Copyright 2024 The Dapr Authors
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#     http://www.apache.org/licenses/LICENSE-2.0
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#

# This script validates resource utilization of a Dapr sidecar.

import re
import subprocess
import time
import os
import numpy as np
import psutil
import requests

from pathlib import Path
from scipy.stats import ttest_ind

def get_binary_size(binary_path):
    try:
        size = os.path.getsize(Path(binary_path).expanduser()) // 1024  # in kilobytes
        return size
    except FileNotFoundError:
        print(f"Could not find file size for {binary_path}")
        return None

def run_process_background(args):
    process = subprocess.Popen(args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    return process

def kill_process(process):
    process.terminate()

def get_memory_info(process):
    try:
        process_info = psutil.Process(process.pid)
        resident_memory = process_info.memory_info().rss / (1024 * 1024)  # in megabytes
        return resident_memory
    except psutil.NoSuchProcess:
        return None
    
def get_goroutine_count():
    try:
        response = requests.get('http://localhost:9090/metrics')
        metrics = response.text

        match = re.search(r'go_goroutines (\d+)', metrics)
        if match:
            goroutine_count = int(match.group(1))
            return goroutine_count
        else:
            raise ValueError("Failed to extract Goroutine count from metrics.")

    except (requests.ConnectionError, IndexError, ValueError):
        return None

def run_sidecar(executable, app_id):
    print(f"Running {executable} ...")
    expanded_executable=Path(executable).expanduser()
    args = [expanded_executable, f"--app-id", f"{app_id}"]

    # Run the process in the background
    background_process = run_process_background(args)

    memory_data = []
    goroutine_data = []

    # Initial wait to remove any noise from initialization.
    time.sleep(10)

    # Collect resident memory every second for X seconds
    cycles = int(getenv("SECONDS_FOR_PROCESS_TO_RUN", 5))
    for _ in range(cycles):
        time.sleep(1)
        memory = get_memory_info(background_process)
        goroutine_count = get_goroutine_count()
        if memory is not None:
            memory_data.append(memory)
        if goroutine_count is not None:
            goroutine_data.append(goroutine_count)
            
    # Kill the process
    kill_process(background_process)

    if len(memory_data) == 0 or len(goroutine_data) == 0:
        raise Exception(f"Could not collect data for {executable}: {( memory_data, goroutine_data)}")

    print(f"Collected metrics for {executable}.")
    return memory_data, goroutine_data

def test_diff(arr_old, arr_new, label, test='ttest'):
    # Output mean and median for memory utilization
    
    p25_new, p50_new, p75_new = np.percentile(arr_new, [25, 50, 75])
    p25_old, p50_old, p75_old = np.percentile(arr_old, [25, 50, 75])

    print(f"Mean for {label} (new): {np.mean(arr_new):.2f}")
    print(f"25th percentile for {label} (new): {p25_new:.2f}")
    print(f"50th percentile (median) for {label} (new): {p50_new:.2f}")
    print(f"75th percentile for {label} (new): {p75_new:.2f}")

    print(f"Mean for {label} (old): {np.mean(arr_old):.2f}")
    print(f"25th percentile for {label} (old): {p25_old:.2f}")
    print(f"50th percentile (median) for {label} (old): {p50_old:.2f}")
    print(f"75th percentile for {label} (old): {p75_old:.2f}")

    if test == 'ttest':
        # Perform t-test to invalidate a > b.
        t_statistic, p_value = ttest_ind(a=arr_new, b=arr_old, alternative="greater", trim=.2)
        print(f"T-Statistic ({label}): {t_statistic}")
        print(f"P-Value ({label}): {p_value}")

        if p_value < 0.05:
            print(f"Warning! Found statistically significant increase in {label}.")
            return True

        print(f"Passed! Did not find statistically significant increase in {label}.")
    elif test == 'tp75_plus_10percent':
        # Memory measurement has enough variation that the t-test is too strict.
        # So, we created this custom comparison to avoid false positives.
        # Picking 10% as a good enough margin observed by various runs with the same binary.
        if p75_new > p75_old * 1.10:
            print(f"Warning! Found significant increase in {label}.")
            return True

        print(f"Passed! Did not find significant increase in {label}.")

    return False

def size_diff(old_binary, new_binary):
    max_diff = int(getenv("LIMIT_DELTA_BINARY_SIZE", 7168)) # in KB, default is 7 MB.
    old_size = get_binary_size(old_binary)
    new_size = get_binary_size(new_binary)
    if new_size > old_size + max_diff:
        print(f"Warning! Significant increase in file size: was {old_size} KB, now {new_size} KB.")
        return True

    print(f"Passed! Did not find significant increase in file size: was {old_size} KB, now {new_size} KB.")
    return False
    

def getenv(key, default):
    v = os.getenv(key)
    if not v or v == "":
        return default
    return v

if __name__ == "__main__":
    goos=getenv("GOOS", "linux")
    goarch=getenv("GOARCH", "amd64")
    new_binary = f"./dist/{goos}_{goarch}/release/daprd"
    old_binary = "~/.dapr/bin/daprd"

    binary_size_diff = size_diff(old_binary, new_binary)

    memory_data_new, goroutine_data_new = run_sidecar(new_binary, "treatment")
    memory_data_old, goroutine_data_old = run_sidecar(old_binary, "control")

    memory_diff = test_diff(memory_data_old, memory_data_new, "memory utilization (in MB)", "tp75_plus_10percent")
    goroutine_diff = test_diff(goroutine_data_old, goroutine_data_new, "number of go routines", "ttest")

    if binary_size_diff or memory_diff or goroutine_diff:
        raise Exception("Found significant differences.")
