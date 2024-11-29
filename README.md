# Omniclip

**Omniclip** is a universal clipboard application that allows seamless clipboard sharing across devices, regardless of
their operating system. Inspired by Apple's Universal Clipboard, Omniclip enables you to copy and paste content between
macOS, Linux, and potentially Windows devices. Its decentralized nature allows nodes to discover each other on a local
network without requiring a central server.

---

## Features

- Cross-platform clipboard sharing.
- Peer-to-peer network topology with automatic discovery of new nodes on a local network.
- Easy setup using a command-line interface.

### Enhancements
- Encrypt data in transit

---

## Installation

### Building from Source

If you'd like to build Omniclip from source:

1. Ensure you have [Go](https://golang.org/dl/) installed.
2. Clone the repository:
   ```shell
   git clone https://github.com/phillipahereza/omniclip.git
   cd omniclip
   ```
3. Build the project:
   ```shell
   go build -o omniclip
   ```
---

## Usage

### Starting the Application

Run the application with the following command:

   ```shell
   omniclip start --topic "my-topic"
   ```

By default, Omniclip will:

1. Start the service on port **49435**.
2. Start a status server on port **49436**.
3. Monitor the clipboard for changes and sync them across connected devices.

You can customize these defaults with the following options:

   ```shell
   omniclip start --port 52321 --status 52322 --topic "my-topic"
   ```

### Checking the Application Status

To check the status of Omniclip, run:

   ```shell
   omniclip status
   ```

### Getting Help

For a complete list of available options, run:

   ```shell
   omniclip help
   ```

---

## License

This project is released into the public domain under the [Unlicense](LICENSE). You are free to copy, modify, and
distribute the software, in either source or binary form, for any purpose.

---

## Contributing

Contributions are welcome! Feel free to open an issue or submit a pull request
on [GitHub](https://github.com/phillipahereza/omniclip).

---
