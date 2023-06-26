sudo rm   ~/.dapr/bin/daprd
sudo rm -R ./dist/linux_amd64/debug
make DEBUG=1 build
cp ./dist/linux_amd64/debug/daprd ~/.dapr/bin/