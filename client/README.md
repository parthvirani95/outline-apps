# <img alt="Outline Client Logo" src="../docs/resources/logo_client.png" title="Outline Client" width="32">&nbsp;&nbsp;Outline Client

![Build and Test](https://github.com/Jigsaw-Code/outline-apps/actions/workflows/build_and_test_debug_client.yml/badge.svg?branch=master) 

The Outline Client is a cross-platform VPN or proxy client for Windows, macOS, iOS, Android, and Debian-based Linux. The Outline Client is designed for use with the [Outline Server](https://github.com/Jigsaw-Code/outline-server) software, but it is fully compatible with any [Shadowsocks](https://shadowsocks.org/) server.

The client's user interface is implemented in [Polymer](https://www.polymer-project.org/) 2.0. Platform support is provided by [Cordova](https://cordova.apache.org/) and [Electron](https://electronjs.org/), with additional native components in this repository.


## Requirements for all builds

All builds require [Node](https://nodejs.org/) 18 (lts/hydrogen), and [Go](https://golang.org/) 1.22 installed in addition to other per-platform requirements.

> 💡 NOTE: if you have `nvm` installed, run `nvm use` to switch to the correct node version!

After cloning this repo, install all node dependencies:

```sh
npm install
```

## Building the shared web app

Outline clients share the same web app across all platforms. This code is located in the src/www directory. If you are making changes to the shared web app and do not need to test platform-specific functionality, you can test in a desktop browser by running:

```sh
npm run action client/src/www/start
```

The latter command will open a browser instance running the app. Browser platform development will use fake servers to test successful and unsuccessful connections.

The app logic is located in [src/www/app](src/www/app). UI components are located in [src/www/ui_components](src/www/ui_components). If you want to work specifically on an individual UI element, try the storybook!:

```sh
npm run action storybook
```

> [!NOTE]
> Every script in this repository can be run with `npm run action` -
> for a CLI-like experience, add something like
>
> ```sh
> alias outline="npm run action"
> ```
>
> _(you can call it whatever you like)_
>
> to your shell, then try `outline www/start`!

## Passing configuration flags to actions

Certain actions take configuration flags - but since we're running them through `npm`, you'll have to use the `--` seperator to funnel them through to the underlying process. For example, to set up a MacOS project in release mode, you'd run:

```sh
SENTRY_DSN=<your sentry dsn> npm run action client/src/cordova/setup macos -- --buildMode=release --versionName=<your version name>
```

## Life of a Packet

[How does the Outline Client work?](/docs/life_of_a_packet.md)

## Accepting a server invite

[Looking for instructions on how to accept a server invite?](/docs/invitation_instructions.md)

## Platform-specific development

Each platform is handled differently:

1. [Developing for Apple **(MacOS and iOS)**](src/cordova/apple)
2. [Developing for **Android**](src/cordova/android)
3. [Developing for Electron **(Windows and Linux)**](electron)

## Error reporting

To enable error reporting through [Sentry](https://sentry.io/) for local builds, run:

```bash
export SENTRY_DSN=[Sentry development API key]
[platform-specific build command]
```

Release builds on CI are configured with a production Sentry API key.
