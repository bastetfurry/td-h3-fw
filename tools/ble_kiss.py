#!/usr/bin/env python3
"""The TD-H3 TNC's KISS over Bluetooth LE, served on a TCP port (as direwolf's KISS TCP), so KISS
clients (direwolf's kissutil, Xastir, QtTermTCP, tools/ scripts) can use the radio without the USB-C
cable, which RF at key-up resets.

The radio's Bluetooth module (BT425, a Telink chip) is a transparent UART to the MCU's UART0 at 9600:
writes to characteristic FF02 arrive on UART0, UART0's bytes come back as FF01 notifications (service
FF00; docs/HARDWARE.md, Bluetooth). With MENU 49 BLUETOOTH on, the TNC (fw/tnc.c) speaks KISS there.

    tools/ble_kiss.py [--name TD-H3 | --address AA:BB:CC:DD:EE:FF] [--port 8101]

Needs bleak (pip install bleak) and a Bluetooth adapter. Bytes pass through unchanged both ways; every
TCP client gets everything the radio sends.
"""
import argparse
import asyncio
import sys

from bleak import BleakClient, BleakScanner

TX_CHAR = '0000ff02-0000-1000-8000-00805f9b34fb'   # to the radio (write without response)
RX_CHAR = '0000ff01-0000-1000-8000-00805f9b34fb'   # from the radio (notify)


class Bridge:
    def __init__(self, args):
        self.args, self.clients, self.ble, self.queue = args, set(), None, asyncio.Queue()

    def log(self, *a):
        print(*a, file=sys.stderr, flush=True)

    def from_radio(self, _h, data):
        for w in list(self.clients):
            try:
                w.write(bytes(data))
            except (ConnectionError, RuntimeError):
                self.clients.discard(w)

    async def client(self, reader, writer):
        self.clients.add(writer)
        self.log('client connected:', writer.get_extra_info('peername'))
        try:
            while data := await reader.read(1024):
                await self.queue.put(data)
        finally:
            self.clients.discard(writer)
            self.log('client gone')

    async def writer(self):
        while True:
            data = await self.queue.get()
            while not (self.ble and self.ble.is_connected):
                await asyncio.sleep(0.2)
            step = max(20, (self.ble.mtu_size or 23) - 3)
            for i in range(0, len(data), step):
                try:
                    await self.ble.write_gatt_char(TX_CHAR, data[i:i + step], response=False)
                except Exception as e:      # noqa: BLE001 - a dropped link: the rest of this write is lost
                    self.log('write failed:', e)
                    break

    async def radio(self):
        while True:
            try:
                dev = self.args.address or await self.find()
                if not dev:
                    await asyncio.sleep(2)
                    continue
                gone = asyncio.Event()
                async with BleakClient(dev, timeout=20, disconnected_callback=lambda _c: gone.set()) as c:
                    self.ble = c
                    await c.start_notify(RX_CHAR, self.from_radio)
                    self.log('radio connected:', c.address, 'MTU', c.mtu_size)
                    await gone.wait()
                self.log('radio disconnected')
            except Exception as e:          # noqa: BLE001 - try again: the radio may be off or out of range
                self.log('BLE:', e)
            self.ble = None
            await asyncio.sleep(2)

    async def find(self):
        d = await BleakScanner.find_device_by_name(self.args.name, timeout=10)
        if not d:
            self.log('no %r found yet (MENU 49 BLUETOOTH on?)' % self.args.name)
        return d

    async def main(self):
        srv = await asyncio.start_server(self.client, self.args.host, self.args.port)
        self.log('KISS TCP on %s:%d' % (self.args.host, self.args.port))
        async with srv:
            await asyncio.gather(srv.serve_forever(), self.radio(), self.writer())


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument('--name', default='TD-H3', help='the radio\'s Bluetooth name (default TD-H3)')
    p.add_argument('--address', help='its address instead of scanning for the name')
    p.add_argument('--host', default='127.0.0.1')
    p.add_argument('--port', type=int, default=8101, help='KISS TCP port (default 8101)')
    try:
        asyncio.run(Bridge(p.parse_args()).main())
    except KeyboardInterrupt:
        pass


if __name__ == '__main__':
    main()
