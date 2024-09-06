version="1.0.1"
installation_path="$HOME/.local/bin/egressguy"
version_file="$installation_path/.version"
binary_file="$installation_path/release"

# check cpu architecture
if [ "$(uname -m)" == "x86_64" ]; then
    goarch="amd64"
# elif [ "$(uname -m)" == "aarch64" ]; then
#     goarch="arm64"
else
    echo "Unsupported architecture: $(uname -m), please install manually."
    exit 1
fi

# we had to notify the user for every command that requires root privileges
privileged_command() {
    echo "Executing command: $@"
    if [ "$EUID" -ne 0 ]; then
        sudo $@ || exit 1
    else
        $@ || exit 1
    fi
}

# check whether libpcap is installed
if [ ! -f /usr/include/pcap.h ] && [ ! -f /usr/share/licenses/libpcap/LICENSE ] && [ ! -f /usr/lib/libpcap.so ]; then
    if [ -f /etc/debian_version ]; then
        privileged_command "apt install libpcap-dev"
    elif [ -f /etc/redhat-release ]; then
        privileged_command "yum install libpcap"
    elif [ -f /etc/arch-release ]; then
        privileged_command "pacman -S libpcap"
    else
        echo "libpcap is not installed. Please install it manually."
        exit 1
    fi
fi

download_latest_release() {
    curl https://github.com/hax0r31337/egressguy/releases/download/$version/egressguy_linux_$goarch -L -o $binary_file || exit 1

    # check ELF header to ensure the binary is valid
    if [ "$(head -c 4 $binary_file)" != $'\x7fELF' ]; then
        echo "Failed to download egressguy binary."
        rm -f $binary_file
        exit 1
    fi

    chmod +x $binary_file
    echo $version > $version_file
}

# download binary from github releases
if [ ! -f $version_file ] || [ ! -f $binary_file ]; then
    mkdir -p $installation_path
    download_latest_release
else
    current_version=$(cat $version_file)
    if [ "$current_version" != "$version" ]; then
        download_latest_release
    fi
fi

# run the binary
privileged_command "$binary_file $@"