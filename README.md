
# Omniclip

**Omniclip** is a universal clipboard application that allows seamless clipboard sharing across devices, regardless of their operating system. Inspired by Apple's Universal Clipboard, Omniclip enables you to copy and paste content between macOS, Linux, and potentially Windows devices. Its decentralized nature allows nodes to discover each other on a local network without requiring a central server.

---

## Features

- Cross-platform clipboard sharing.
- Peer-to-peer network topology with automatic discovery of new nodes on a local network.
- Easy setup using a command-line interface.

---

## Installation

Pre-built binaries for Linux, macOS, and Windows are available for download.

### Steps to Install

1. Download the appropriate binary for your platform from the [Releases](https://github.com/ahrza/omniclip/releases) page.
2. Make the binary executable:
    - **Linux/macOS**: Run `chmod +x omniclip`
    - **Windows**: The binary is executable as-is.
3. Move the binary to a directory in your system's `PATH`:
    - **Linux/macOS**: Run `sudo mv omniclip /usr/local/bin/`
    - **Windows**: Add the binary's location to your system's `PATH`.

### Building from Source

If you'd like to build Omniclip from source:

1. Ensure you have [Go](https://golang.org/dl/) installed.
2. Clone the repository:
   ```bash
   git clone https://github.com/ahrza/omniclip.git
   cd omniclip
   ```
3. Build the project:
   ```bash
   go build -o omniclip
   ```

---

## Usage

### Starting the Application

Run the application with the following command:
   ```bash
   omniclip start
   ```

By default, Omniclip will:
1. Start the service on port **49435**.
2. Start a status server on port **49436**.
3. Use the default topic: **omniclip_X6r9V1NsdGL5Kcfw**.
4. Monitor the clipboard for changes and sync them across connected devices.

You can customize these defaults with the following options:
   ```bash
   omniclip start --port 52321 --status 52322 --topic "my-topic"
   ```

### Checking the Application Status

To check the status of Omniclip, run:
   ```bash
   omniclip status
   ```

### Getting Help

For a complete list of available options, run:
   ```bash
   omniclip help
   ```

---

## License

This project is released into the public domain under the [Unlicense](LICENSE). You are free to copy, modify, and distribute the software, in either source or binary form, for any purpose.

---

## Contributing

We welcome contributions! Feel free to open an issue or submit a pull request on [GitHub](https://github.com/ahrza/omniclip).

---
