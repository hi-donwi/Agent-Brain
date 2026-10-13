# Troubleshooting Guide - lokal Doctor Checks

`lokal doctor` runs diagnostic checks across your environment, hardware, storage, and
inference backends. This guide explains each check ID and provides actionable fixes.

---

## 1. Check: `hardware`

- **Purpose:** Verifies system architecture, CPU cores, unified memory or RAM, and GPU/Metal accelerators.
- **Possible Warnings/Failures:**
  - *No accelerator detected on Linux:* inference runs via CPU AVX2/AVX-512.
- **Remediation:**
  - On macOS: ensure Apple Silicon Metal support is enabled.
  - On Linux with NVIDIA GPU: install NVIDIA container toolkit and drivers (`nvidia-smi`).
  - On headless CPU VMs: configure `profile: tiny` in `~/.lokal/config.yaml` to use lightweight quantized models.

---

## 2. Check: `home`

- **Purpose:** Ensures the `.lokal` state directory is present, writable, and mode-restricted (`0700`).
- **Possible Failures:**
  - `home directory not writable`
  - `LOKAL_HOME is not an absolute path`
- **Remediation:**
  ```bash
  # Ensure home layout exists with correct permissions
  mkdir -p ~/.lokal
  chmod 700 ~/.lokal

  # Or configure an explicit absolute path on a larger disk
  export LOKAL_HOME=/mnt/data/lokal
  ```

---

## 3. Check: `disk`

- **Purpose:** Checks free disk space on the partition hosting `~/.lokal` against the 2.0 GiB safety margin.
- **Possible Warnings:**
  - `free disk space below 2.0 GiB`
- **Remediation:**
  - Free disk space on the primary drive.
  - Remove unused models:
    ```bash
    lokal models rm <unused-model>
    ```
  - Mount `.lokal` on an external or secondary drive by setting `LOKAL_HOME=/path/to/disk/.lokal`.

---

## 4. Check: `config`

- **Purpose:** Verifies that `config.yaml` exists, is valid YAML, and conforms to version 1 schema.
- **Possible Warnings:**
  - `config.yaml not created yet; lokal setup creates it`
- **Remediation:**
  ```bash
  # Initialize standard configuration
  lokal setup --profile tiny --dry-run
  lokal setup --yes
  ```

---

## 5. Check: `backend`

- **Purpose:** Pings the primary inference backend (Ollama, llama.cpp, etc.) over loopback HTTP.
- **Possible Failures:**
  - `backend not reachable at http://127.0.0.1:11434`
- **Remediation:**
  - Start the backend daemon:
    ```bash
    # On macOS via Homebrew
    brew services start ollama

    # On Linux via systemd
    sudo systemctl start ollama

    # Or manually in background
    ollama serve &
    ```
  - If using a remote gateway over SSH tunnel:
    ```bash
    ssh -N -L 11434:127.0.0.1:11434 user@remote-box
    ```

---

## 6. Check: `models`

- **Purpose:** Ensures that all aliases in `config.yaml` (`chat`, `code`, `embed`, etc.) resolve to present models.
- **Possible Failures:**
  - `alias 'code' resolves to 'qwen2.5-coder-1.5b' but model is missing`
- **Remediation:**
  ```bash
  # Pull the missing model through lokal
  lokal models pull qwen2.5-coder:1.5b

  # Or re-run automated setup
  lokal setup --yes
  ```

---

## 7. Check: `memory`

- **Purpose:** Verifies that the primary model's context length fits within the physical memory budget.
- **Possible Warnings:**
  - `model memory exceeds residency limit`
- **Remediation:**
  - Switch to a smaller hardware profile in `~/.lokal/config.yaml` (`tiny` uses ~1.2 GiB).
  - Reduce `num_ctx` in backend options or adjust `residency.max_memory_bytes`.

---

## 8. Check: `smoke`

- **Purpose:** Executes an end-to-end inference smoke test (one test completion and one embedding) and reports tokens/second.
- **Possible Failures:**
  - `smoke test chat completion timed out`
- **Remediation:**
  - Check system CPU/RAM load.
  - Verify that the backend is responsive with `curl http://127.0.0.1:11434/api/tags`.
  - Re-run `lokal doctor` after closing heavy memory-consuming applications.
