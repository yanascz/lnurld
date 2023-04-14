# LNURL Daemon

LNURL Daemon is a minimalistic [Lightning Address](https://lightningaddress.com/) and LNURL self-hosted HTTP server.
It is intended to run on your node and connect directly to your [LND](https://github.com/lightningnetwork/lnd).
You may test it by sending some sats to ⚡lnurld@yanas.cz or by scanning the following QR code:\
![LNURL-pay QR code](https://yanas.cz/ln/pay/lnurld/qr-code)

## Supported features

* [LUD-01: Base LNURL encoding](https://github.com/fiatjaf/lnurl-rfc/blob/luds/01.md)
* [LUD-06: `payRequest` base spec](https://github.com/fiatjaf/lnurl-rfc/blob/luds/06.md)
* [LUD-09: `successAction` field for `payRequest`](https://github.com/fiatjaf/lnurl-rfc/blob/luds/09.md)
* [LUD-12: Comments in `payRequest`](https://github.com/fiatjaf/lnurl-rfc/blob/luds/12.md)
* [LUD-16: Paying to static internet identifiers](https://github.com/fiatjaf/lnurl-rfc/blob/luds/16.md)
* Multiple customizable accounts
* Lightning Network terminal
* Lightning Network raffle

## Installation

LND is expected to run on the same machine and user `bitcoin` is assumed.
You also need [Go installed](https://go.dev/doc/install).

### Build from source

```shell
$ git clone https://github.com/yanascz/lnurld.git
$ cd lnurld
$ go install
$ go build
```

### Create config file

```shell
$ sudo mkdir /etc/lnurld
$ sudo chown bitcoin:bitcoin /etc/lnurld
$ sudo -u bitcoin vim /etc/lnurld/config.yaml
```

Example configuration with one admin, one user with restricted access, one donate account and one raffle account:

```yaml
credentials:
  admin: 4dm!nS3cr3t
  guest: S3cr3t
access-control:
  guest: [raffle]
accounts:
  satoshi:
    min-sendable: 1
    max-sendable: 1_000_000
    description: Sats for Satoshi
    is-also-email: true
    comment-allowed: 210
  raffle:
    description: Raffle ticket
    thumbnail: raffle.png
    raffle:
      ticket-price: 21_000
      prizes:
        - Trezor Model T: 1
        - Trezor Model One: 2
        - Trezor Lanyard: 5
```

(Create image `raffle.png` in `/etc/lnurld/thumbnails` if you want it served by `lnurld`.)

Available configuration properties:

| Property                         | Description                                                                 | Default value                                              |
|:---------------------------------|:----------------------------------------------------------------------------|:-----------------------------------------------------------|
| `listen`                         | Host and port to listen on.                                                 | `127.0.0.1:8088`                                           |
| `thumbnail-dir`                  | Directory where to look for thumbnails.                                     | `/etc/lnurld/thumbnails`                                   |
| `data-dir`                       | Directory where invoice payment hashes per account will be stored.          | `/var/lib/lnurld`                                          |
| `lnd`                            | Configuration of your LND node.                                             | _see below_                                                |
| `lnd.address`                    | Host and port of gRPC API interface.                                        | `127.0.0.1:10009`                                          |
| `lnd.cert-file`                  | Path to TLS certificate.                                                    | `/var/lib/lnd/tls.cert`                                    |
| `lnd.macaroon-file`              | Path to invoice macaroon.                                                   | `/var/lib/lnd/data/chain/bitcoin/mainnet/invoice.macaroon` |
| `credentials`                    | Map of users authorized to access the admin user interface.                 | _none_                                                     |
| `access-control`                 | Map of accounts accessible by non-admin users.                              | _none, i.e. all users have full admin access_              |
| `accounts`                       | Map of available accounts.                                                  | _none_                                                     |
| `accounts.*.currency`            | Terminal currency; `cad`, `chf`, `czk`, `eur`, `gbp` and `usd` supported.   | `eur`                                                      |
| `accounts.*.max-sendable`        | Maximum sendable amount in sats. _(not available for raffle)_               | _none_                                                     |
| `accounts.*.min-sendable`        | Minimum sendable amount in sats. _(not available for raffle)_               | _none_                                                     |
| `accounts.*.description`         | Description of the account.                                                 | _none_                                                     |
| `accounts.*.thumbnail`           | Name of PNG/JPEG thumbnail to use; 256×256 pixels recommended. _(optional)_ | _none_                                                     |
| `accounts.*.is-also-email`       | Does the account match an email address?                                    | `false`                                                    |
| `accounts.*.comment-allowed`     | Maximum length of invoice comment.                                          | `0`                                                        |
| `accounts.*.archivable`          | May the account storage file be archived on demand?                         | `false`                                                    |
| `accounts.*.raffle`              | Raffle configuration. _(optional)_                                          | _none_                                                     |
| `accounts.*.raffle.ticket-price` | Price of a ticket in sats.                                                  | _none_                                                     |
| `accounts.*.raffle.prizes`       | List of prize/quantity pairs.                                               | _none_                                                     |

If a property is marked as optional or has a default value, you don’t have to specify it explicitly.

### Create data directory

```shell
$ sudo mkdir -m 710 /var/lib/lnurld
$ sudo chown bitcoin:bitcoin /var/lib/lnurld
```

Payment hashes of invoices per account will be stored there.

### Run the server

```shell
$ ./lnurld
```

Alternatively with a custom config file path:

```shell
$ ./lnurld --config=/home/satoshi/.lnurld/cfg.yaml
```

Don’t forget to stop the server before setting up systemd service!

### Setup systemd service

```shell
$ sudo cp lnurld /usr/local/bin
$ sudo cp systemd/lnurld.service /lib/systemd/system
$ sudo systemctl start lnurld.service
$ sudo systemctl enable lnurld.service
```

Now the service should be up and running, listening on configured host and port.

### Setup reverse proxy

Example [nginx](https://nginx.org) configuration for domain `nakamoto.example` (replace with your own) with
[Let’s Encrypt](https://letsencrypt.org) SSL certificate:

```
http {
    #
    # general configuration omitted
    # 

    upstream lnurld {
        server 127.0.0.1:8088;
    }

    server {
        listen       443 ssl http2;
        listen       [::]:443 ssl http2;
        server_name  nakamoto.example;

        ssl_certificate      "/etc/letsencrypt/live/nakamoto.example/fullchain.pem";
        ssl_certificate_key  "/etc/letsencrypt/live/nakamoto.example/privkey.pem";
        ssl_session_cache    shared:lnurld:1m;

        proxy_set_header  X-Forwarded-Proto $scheme;
        proxy_set_header  X-Forwarded-Host $host;

        location /.well-known/lnurlp/ {
            proxy_pass http://lnurld;
        }
        location /ln/ {
            proxy_pass http://lnurld;
        }
    }
}
```

If you don’t have an SSL certificate, you can get one using [Certbot](https://certbot.eff.org).

## Usage

Once configured and deployed, you shall be able to send sats to ⚡satoshi@nakamoto.example from your LN wallet.
If you need to display a QR code, simply navigate to or share https://nakamoto.example/ln/pay/satoshi/qr-code.
For a smaller/larger QR code, feel free to append desired size in pixels to the URL, e.g `?size=1024`.

Same applies to ⚡raffle@nakamoto.example and https://nakamoto.example/ln/pay/raffle/qr-code with raffle configured.
These allow anyone to purchase as many raffle tickets for the configured price as they wish, increasing their chances.
Once enough tickets are sold, i.e. at least the same number as there are prizes, you may start drawing winning tickets
from the account’s detail page.

To see amount of received sats or raffle for accessible accounts, navigate to https://nakamoto.example/ln/accounts.
You’ll need to authenticate using one of the configured username/password pairs. Account stats, QR code and/or payment
terminal are accessible from the account’s detail page.

**The raffle is stateless so refreshing its page restarts the draw and may produce different winning tickets!**

## Update

```shell
$ cd lnurld
$ git pull
$ ./systemd/deploy.sh
```

Alternatively checkout a specific branch/tag.
