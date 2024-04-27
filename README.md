# LNURL Daemon

LNURL Daemon is a minimalistic [Lightning Address](https://lightningaddress.com/) and LNURL self-hosted HTTP server.
It is intended to run on your node and connect directly to your [LND](https://github.com/lightningnetwork/lnd).
You may test it by sending some sats to ⚡lnurld@yanas.cz or by scanning the following QR code:\
![LNURL-pay QR code](https://yanas.cz/ln/pay/lnurld/qr-code)

## Supported features

* [LUD-01: Base LNURL encoding](https://github.com/fiatjaf/lnurl-rfc/blob/luds/01.md)
* [LUD-03: `withdrawRequest` base spec](https://github.com/fiatjaf/lnurl-rfc/blob/luds/03.md)
* [LUD-04: `auth` base spec](https://github.com/fiatjaf/lnurl-rfc/blob/luds/04.md)
* [LUD-06: `payRequest` base spec](https://github.com/fiatjaf/lnurl-rfc/blob/luds/06.md)
* [LUD-09: `successAction` field for `payRequest`](https://github.com/fiatjaf/lnurl-rfc/blob/luds/09.md)
* [LUD-12: Comments in `payRequest`](https://github.com/fiatjaf/lnurl-rfc/blob/luds/12.md)
* [LUD-16: Paying to static internet identifiers](https://github.com/fiatjaf/lnurl-rfc/blob/luds/16.md)
* Multiple customizable accounts
* Lightning Network terminal
* Lightning Network raffle
* Events with LNURL-auth sign-up

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

Example configuration with one admin, one user with restricted access and two accounts:

```yaml
credentials:
  satoshi: 4dm!nS3cr3t
  barista: S3cr3t
administrators: [satoshi]
access-control:
  barista: [cafe]
accounts:
  satoshi:
    min-sendable: 1
    max-sendable: 1_000_000
    description: Sats for Satoshi
    is-also-email: true
    comment-allowed: 210
  cafe:
    currency: usd
    min-sendable: 1
    max-sendable: 1_000_000
    description: Bitcoin Café 
    thumbnail: cafe.png
```

(Create image `cafe.png` in `/etc/lnurld/thumbnails` if you want it served by `lnurld`.)

Available configuration properties:

| Property                        | Description                                                               | Default value                                               |
|:--------------------------------|:--------------------------------------------------------------------------|:------------------------------------------------------------|
| `listen`                        | Host and port to listen on.                                               | `127.0.0.1:8088`                                            |
| `thumbnail-dir`                 | Directory with PNG/JPEG thumbnails; 256×256 pixels recommended.           | `/etc/lnurld/thumbnails`                                    |
| `data-dir`                      | Directory where payment hashes and other data will be stored.             | `/var/lib/lnurld`                                           |
| `lnd`                           | Configuration of your LND node.                                           | _see below_                                                 |
| `lnd.address`                   | Host and port of gRPC API interface.                                      | `127.0.0.1:10009`                                           |
| `lnd.cert-file`                 | Path to TLS certificate.                                                  | `/var/lib/lnd/tls.cert`                                     |
| `lnd.macaroon-file`             | Path to macaroon file to use.                                             | `/var/lib/lnd/data/chain/bitcoin/mainnet/invoices.macaroon` |
| `credentials`                   | Map of users authorized to access the admin user interface.               | _none_                                                      |
| `administrators`                | List of admin users with access to all accounts, events and raffles.      | _none,                                                      |
| `access-control`                | Map of accounts accessible by non-admin users.                            | _none,                                                      |
| `thumbnails`                    | Map of raffle thumbnail files per user.                                   | _none,                                                      |
| `accounts`                      | Map of available accounts.                                                | _none_                                                      |
| `accounts.*.currency`           | Terminal currency; `cad`, `chf`, `czk`, `eur`, `gbp` and `usd` supported. | `eur`                                                       |
| `accounts.*.max-sendable`       | Maximum sendable amount in sats.                                          | _none_                                                      |
| `accounts.*.min-sendable`       | Minimum sendable amount in sats.                                          | _none_                                                      |
| `accounts.*.description`        | Description of the account.                                               | _none_                                                      |
| `accounts.*.thumbnail`          | Name of thumbnail file to use. _(optional)_                               | _none_                                                      |
| `accounts.*.is-also-email`      | Does the account match an email address?                                  | `false`                                                     |
| `accounts.*.comment-allowed`    | Maximum length of invoice comment.                                        | `0`                                                         |
| `accounts.*.archivable`         | May the account storage file be archived on demand?                       | `false`                                                     |
| `authentication`                | Configuration of LN authentication.                                       | _see below_                                                 |
| `authentication.request-expiry` | Expiry of authentication requests in specified units.                     | `90s`                                                       |
| `withdrawal`                    | Configuration of LN withdrawals.                                          | _see below_                                                 |
| `withdrawal.fee-percent`        | Withdrawal fee in percent to cover withdrawal costs.                      | `0`                                                         |
| `withdrawal.request-expiry`     | Expiry of withdrawal requests in specified units.                         | `90s`                                                       |

If a property is marked as optional or has a default value, you don’t have to specify it explicitly.

### Create data directory

```shell
$ sudo mkdir /var/lib/lnurld
$ sudo chown bitcoin:bitcoin /var/lib/lnurld
```

Account, event and raffle data will be stored there.

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

        location / {
            proxy_pass http://lnurld;
        }
    }
}
```

If you don’t have an SSL certificate, you can get one using [Certbot](https://certbot.eff.org).

## Usage

Once configured and deployed, you shall be able to send sats to ⚡satoshi@nakamoto.example from your LN wallet.
If you need to display a QR code, simply navigate to or share https://nakamoto.example/ln/pay/satoshi/qr-code.
For a smaller/larger QR code, feel free to append desired size in pixels to the URL, e.g `?size=1024`. This pattern
applies to any configured account.

To see accessible accounts and to manage events/raffles, navigate to https://nakamoto.example/auth. You’ll need
to authenticate using one of the configured username/password pairs. Account stats, QR code and/or payment terminal
are accessible from the account’s detail page in the Accounts section at https://nakamoto.example/auth/accounts.

Events may be managed in the Events section at https://nakamoto.example/auth/events. Each created event may be shared
with your friends, and they may sign up to attend the event once they authenticate using their LN wallet.

Raffles may be managed in the Raffles section at https://nakamoto.example/auth/raffles. Raffle QR code may be shared
to allow anyone to purchase as many raffle tickets as they wish, increasing their chances. Once enough tickets are sold,
i.e. at least the same number as there are prizes, you may start drawing winning tickets from the raffle’s detail page.

Once a raffle is drawn, received sats may be withdrawn to any LN wallet that supports LNURL-withdraw. However, you have
to first configure path to a macaroon with `invoices:read invoices:write offchain:read offchain:write` permissions.

## Update

```shell
$ cd lnurld
$ git pull
$ ./systemd/deploy.sh
```

Alternatively checkout a specific branch/tag.
