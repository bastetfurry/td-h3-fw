# TNC (packet radio and APRS)

## Quick reference

| I want to… | Press |
|---|---|
| Turn the TNC on | Hold **8**, or **MENU**, **3** **1** (TNC), **MENU** (in the TNC firmware) |
| Leave it | **V/M** |
| Hear the packets on the speaker | **#** (again: quiet); it stays on when the TNC sends |
| Change the frequency | Type it, as on the home screen (**1** **4** **5** **5** **0** **0** is 145.500; a channel number on a memory channel); **V/M** takes a digit back. **UP** / **DOWN** step it |
| Lock the keys | Hold **\*** (again to unlock): every key, the side keys and the flashlight too |

The TNC turns the radio into a packet modem for a phone or a computer. An app such as APRSdroid
(Android), Xastir or Direwolf's KISS clients talks to it over the USB-C cable; the radio sends
their packets on the air and passes every packet it hears to them. Packets are AFSK 1200 baud
(the APRS standard, Bell 202), framed as AX.25. Every kind of AX.25 frame goes through, not only
APRS's, so a terminal program that speaks AX.25 over KISS (Linpac, Paracon, QtTermTCP) can also
connect to a packet BBS or node with it.

## What it can do

- **A KISS TNC for AX.25 at 1200 baud** (AFSK, Bell 202): APRS and connected-mode packet alike.
  Every frame type (UI, I, S and U) goes both ways, so APRS apps, BBS and node software and AX.25
  terminals all work through it; the app on the phone or computer runs the protocol.
- **Frames up to AX.25's full 330 bytes** (8 digipeaters and 256 bytes of data), sent and received.
- **Up to 4 frames queued** and sent in one transmission: connected mode with a window (MAXFRAME) of 4.
- **KISS commands**: data on port 0, TXDELAY, P, SLOTTIME, TXTAIL, FULLDUPLEX, ACKMODE (0x0C), and
  the `TXBUF:` and `TNC:` queries.
- **Listens before it sends**: it waits while a packet is coming in, even one too weak to decode
  (it recognises a packet's start flags within about 25 ms), and while a carrier is on the channel as
  SQUELCH sees it. Then KISS P and SLOTTIME pick the moment.
- **USB-C (38400 baud) or Bluetooth LE (9600 baud)** to the app.
- **FX.25 frames are received** as the plain AX.25 frame inside them (no error correction).
- **A screen that shows what happens**: frequency, last station and its signal, counts, the send
  state and queue, lights for packets coming in, decoded, lost and sent. The frequency can be changed
  without leaving.
- **No hardware change**: the radio hears the audio through the beep's pin.

## What it can't do

- **1200 baud only**: no 300 baud (HF) and no 9600 baud.
- **No error correction**: FX.25 frames are decoded only when their AX.25 part arrives intact, and
  it never sends FX.25.
- **Nothing on its own**: no beacon, digipeater, IGate or connected-mode stack in the radio. The app
  does all of that.
- **Receiving is weaker than a dedicated TNC's.** The beep pin gives one bit of audio, not a real
  audio input: strong, clean packets decode well (30 of 30 a metre away), weak or distorted ones
  are missed more often.
- **One channel, half duplex**, on the selected slot, FM only. It sends only where PTT may (TX
  BANDS) and only with the factory calibration.
- **Voice or other signals it can't hear well enough**: at SQUELCH 0 it notices only a carrier about
  12 dB over the band's noise, and a carrier that never stops stops counting after a while. It can't
  hear a station that the other end hears and it doesn't (the hidden station).
- **The KISS settings aren't saved**: they start from the defaults each time the TNC opens.
- **A firmware of its own**, built from the source: no FM radio, weather, SCOPE, FREQ FIND or
  beeps in it, and no REMOTE while the TNC is open.

**The TNC is in its own firmware image, not in the normal one.** That firmware has no FM radio,
no weather (WEATHER, WX ALERT), no SCOPE and no FREQ FIND: the room went to the TNC. Long **8** opens the TNC there, and
the menu has it as item 31; SCOPE, FREQ FIND, FM RADIO, WEATHER and WX ALERT are left out of its menu. It's for trying out; the normal firmware is what the flasher puts
on the radio. To build and flash it from the source:

```bash
make -C fw tnc                     # fw/build/td-h3-tnc.bin: transmits within TX BANDS
make -C fw tnc-rx                  # fw/build/td-h3-tnc-rx.bin: receive only
tdh3 firmware flash --force fw/build/td-h3-tnc.bin
```

Flash the normal firmware again the same way, or with `tdh3 update`, to get the FM radio and weather back.

## Setting it up

1. Tune the selected slot to the packet frequency: **144.800 MHz** for APRS in Europe, **144.390**
   in North America.
2. **BANDWIDTH** (item 5): **WIDE** for sending. APRS stations use wide; on narrow the radio's
   transmit audio filter cuts the 2200 Hz tone and other stations decode fewer of your packets.
3. **POWER** (item 9) as low as gets you heard: a packet is short, but the TNC may send often.
4. **TX BANDS** (item 13) must include the band, as for talking: the TNC transmits only where PTT
   would (see [Transmitting](Transmitting)).
5. **SQUELCH** (item 1): 0 or a low setting suits packet best. It decides when the TNC sees a
   carrier on the channel, never what it decodes (see [When it sends](#when-it-sends)).
6. Hold **8**. The TNC screen comes up.
7. Connect the phone or computer with the USB-C cable and start the app:
   - **APRSdroid**: Preferences, Connection: protocol **TNC (KISS)**, type **USB serial**, speed
     **38400**. A phone needs a USB OTG adapter.
   - **A computer**: the radio's serial port (on Linux `/dev/ttyUSB0`, on Windows a COM port) at
     **38400 baud**, KISS.

## When it sends

The TNC sends when the channel is free: no packet coming in and no carrier. Then each KISS
SLOTTIME it takes the channel with a chance of P in 256, and it looks once more right before it keys
up. FULLDUPLEX skips all of this.

- **A packet coming in**, whatever SQUELCH is: the TNC recognises a packet's start flags within
  about 25 ms, even one too weak to decode, and waits until about 170 ms after its end.
- **A carrier**, as SQUELCH sets it:

| SQUELCH | A carrier is… |
|---|---|
| 0 | a signal about 12 dB over the band's noise floor. The TNC learns the floor itself: quickly downwards, slowly upwards (1 dB in 2 s), so a carrier that never ends stops counting after a while |
| 1-9 | the squelch open, as on the home screen (signal strength and how quiet the audio gets) |
| S1-S9 | the squelch open and the S-meter at that level or over (S9 = -93 dBm, 6 dB an S-unit) |

SQUELCH never mutes what the TNC decodes, and the speaker is on **#** alone. `BUSY` on the screen
lights while a packet or a carrier is on the channel.

## The screen

| Line | Shows |
|---|---|
| `TNC` and the frequency | The slot it listens and sends on; top right the battery, as on the home screen (red at 20 % or less) |
| `KISS USB 38400` or `KISS BLUETOOTH 9600` | Where the app connects: the cable, or Bluetooth with BLUETOOTH on |
| `RX    12  TX     3` | Packets heard and passed to the app, packets sent, since the TNC opened (up to 65535 each) |
| `LAST N0CALL-9 -43dBm` | The station of the last packet heard and how strong it came in (`NOTHING HEARD YET` before the first; a long callsign drops the word `LAST`) |
| `TX IDLE` | The sending side, always in the same place: `IDLE` (nothing to send), `WAIT` (packets wait for a free channel), `KEYING` and `SENDING` (on the air), `REFUSED` in red (outside TX BANDS, no factory calibration, or AM/USB; it stays until a packet goes out) |
| `QUEUE 0 BUSY DROP 0` | Three fields that never move, dim while there's nothing to say: `QUEUE` how many packets from the app wait (up to 4); `BUSY` lights while the channel is in use; `DROP` counts packets it couldn't take, in red (4 were already waiting, or one was too long; up to 99) |

The lights show the traffic:

| Light | Means |
|---|---|
| Left green | A packet is coming in (the TNC hears its start flags, then its bytes) |
| Right green, 0.3 s | A packet decoded and passed to the app |
| Right red, 0.3 s | A packet came in but didn't decode (too weak, or a collision) |
| Both red | The TNC is sending |

With BLUETOOTH on, the Bluetooth module can light the right spot by itself as well: it's wired to it.

The backlight follows **LIGHT** as on the home screen: on while a signal is in and while the TNC
sends, then for LIGHT's time; the first key on a dark screen only lights it.

While the TNC is open the rest of the radio waits: dual watch, the priority channel, WX ALERT, VOX
and scanning pause, and AUTO OFF doesn't switch it off. They carry on when you leave with **V/M**.

## Good to know

- **Bluetooth instead of the cable.** With **BLUETOOTH** on (menu 49) the TNC talks KISS over the
  radio's Bluetooth LE module instead of USB-C (the screen says `BLUETOOTH 9600`). A computer
  reaches it through `tools/ble_kiss.py`, which offers it as KISS on TCP port 8101 for direwolf's
  clients, Xastir or QtTermTCP. Bluetooth doesn't drop when the radio transmits, as the cable can.
  Phone apps need to support a plain BLE serial link (service FF00, write FF02, notify FF01);
  APRSdroid's Bluetooth TNC support may not cover it.
- **The USB link can drop when the radio transmits.** Radio energy on the cable resets the USB
  chip; the app then loses the connection. A clip-on ferrite on the cable at the radio end (the
  cable looped through it 2 to 4 times), the antenna away from the cable, and a short cable help.
- **Receiving is simpler than a real TNC's.** The radio has no proper audio input for the computer
  chip, so the TNC hears the audio through a pin meant for the beep (see
  [docs/HARDWARE.md](https://github.com/limehawk/td-h3-fw/blob/main/docs/HARDWARE.md), "P4.6 as an
  audio input"). Strong, clean packets decode well; weak or distorted ones are missed more often
  than by a dedicated TNC. For now it hears best with the slot on **NARROW**, while sending needs
  **WIDE**.
- **Up to 4 packets wait to go out.** The radio sends all of them in one transmission, one after
  another, so a connected-mode app (a BBS or node client) can use a window (MAXFRAME) of up to 4.
  A packet the app sends while 4 wait is dropped (`DROP`). A packet may be AX.25's longest, 330
  bytes (8 digipeaters and 256 bytes of text): longer ones are dropped.
- **No beeps in the TNC.** The beep's pin and timer are the TNC's audio input and clock.
- **The app's settings work**: KISS TXDELAY (how long the radio sends a tone before the packet),
  P and SLOTTIME (how eager it is to take a free channel), TXTAIL (how long it keeps sending after the
  last packet) and FULLDUPLEX (send without waiting for a quiet channel). They start from the usual
  defaults each time the TNC opens; apps send their own when they connect. The tone levels are fixed:
  measured so that other radios and Dire Wolf decode the TD-H3 well.
- **ACKMODE** (G8BPQ's KISS command 0x0C, for LinBPQ and other node software): a packet sent that way
  is answered with its two id bytes once it has gone out on the air, so the software knows when to send
  more.
- **`TXBUF:` and `TNC:`** (KISS SetHardware, as Dire Wolf answers them): `C0 06 TXBUF: C0` gets back how
  many bytes still wait to go out, `TXBUF:00660`, so an app can pace its packets; `C0 06 TNC: C0` gets
  `TNC:TD-H3` and the firmware version. Other SetHardware text is ignored.
- **KISS is read strictly:** a packet with a broken escape (FESC followed by anything but TFEND or TFESC)
  is dropped, and so is anything for a port other than 0; unknown commands are skipped.

- **REMOTE** doesn't work while the TNC is open: the TNC has the USB port. Leave it (**V/M**) to
  flash or to use `tdh3`.
