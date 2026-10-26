VERSION := $(shell tr -d '\r\n' < VERSION)
PKG_VERSION := 1.0.0.2
MACOS_MIN_VERSION := 13.0
ifeq ($(shell uname -s),Darwin)
export MACOSX_DEPLOYMENT_TARGET=$(MACOS_MIN_VERSION)
endif
CC ?= cc
CFLAGS ?= -std=c11 -Wall -Wextra -O2
PREFIX ?= /usr/local
BIN := build/pubcraft
SRC := src/pubcraft.c
GUI := build/pubcraftgui
GUISRC := $(wildcard src/pubcraftgui/*.go) src/pubcraftgui/go.mod src/pubcraftgui/go.sum
GUIWEB := $(shell find src/pubcraftgui/web -type f)
FONTFILES := $(shell find fonts -type f)
THIRDPARTY := $(shell find third-party-licenses -type f)
HTML2PDF := build/pubcraft-html2pdf
APP := build/PubCraft.app
DMG := build/PubCraft-$(VERSION).dmg
export COPYFILE_DISABLE=1

all: $(BIN) $(GUI) $(HTML2PDF)

$(BIN): $(SRC)
	@mkdir -p build
	$(CC) $(CFLAGS) -o $@ $<

$(GUI): $(GUISRC) $(GUIWEB)
	@mkdir -p build
	go -C src/pubcraftgui build -trimpath -o ../../$(GUI) .

$(HTML2PDF): src/pubcraft-html2pdf.m
	@mkdir -p build
	@if [ "$$(uname -s)" = Darwin ]; then clang -O2 -fmodules -framework Cocoa -framework WebKit -o $@ $<; fi

install: all
	install -d $(DESTDIR)$(PREFIX)/bin $(DESTDIR)$(PREFIX)/share/man/man1 $(DESTDIR)$(PREFIX)/share/pubcraft/fonts $(DESTDIR)$(PREFIX)/share/doc/pubcraft
	install -m 755 $(BIN) $(GUI) src/pubcraft-mcp $(DESTDIR)$(PREFIX)/bin/
	cp -R fonts/. $(DESTDIR)$(PREFIX)/share/pubcraft/fonts/
	cp -R third-party-licenses $(DESTDIR)$(PREFIX)/share/doc/pubcraft/
	@if [ -f $(HTML2PDF) ]; then install -m 755 $(HTML2PDF) $(DESTDIR)$(PREFIX)/bin/; fi
	install -m 644 man/pubcraft.1 $(DESTDIR)$(PREFIX)/share/man/man1/

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/pubcraft $(DESTDIR)$(PREFIX)/bin/pubcraftgui $(DESTDIR)$(PREFIX)/bin/pubcraft-mcp $(DESTDIR)$(PREFIX)/bin/pubcraft-html2pdf $(DESTDIR)$(PREFIX)/share/man/man1/pubcraft.1

# Always assemble all resources BEFORE signing. This is ad-hoc signing only;
# Developer ID signing/notarization requires the publisher's own credentials.
app: all $(FONTFILES) $(THIRDPARTY)
	rm -rf $(APP)
	osacompile -o $(APP) src/PubCraft.applescript
	cp $(BIN) $(GUI) $(HTML2PDF) $(APP)/Contents/MacOS/
	cp -R fonts $(APP)/Contents/Resources/fonts
	cp -R third-party-licenses $(APP)/Contents/Resources/
	cp src/AppIcon.icns $(APP)/Contents/Resources/droplet.icns
	cp src/AppIcon.icns $(APP)/Contents/Resources/applet.icns
	/usr/libexec/PlistBuddy -c "Add :CFBundleShortVersionString string 1.0.0" $(APP)/Contents/Info.plist
	/usr/libexec/PlistBuddy -c "Add :CFBundleVersion string 2" $(APP)/Contents/Info.plist
	/usr/libexec/PlistBuddy -c "Add :CFBundleGetInfoString string $(VERSION)" $(APP)/Contents/Info.plist
	/usr/libexec/PlistBuddy -c "Add :LSMinimumSystemVersion string $(MACOS_MIN_VERSION)" $(APP)/Contents/Info.plist
	/usr/libexec/PlistBuddy -c "Set :LSMinimumSystemVersionByArchitecture:x86_64 $(MACOS_MIN_VERSION)" $(APP)/Contents/Info.plist
	codesign --force --sign - $(APP)/Contents/MacOS/pubcraft
	codesign --force --sign - $(APP)/Contents/MacOS/pubcraftgui
	codesign --force --sign - $(APP)/Contents/MacOS/pubcraft-html2pdf
	codesign --force --sign - $(APP)
	codesign --verify --deep --strict $(APP)

# Unique disposable staging directories prevent stale/personal files leaking
# from a previous packaging attempt. Stop immediately on any failed command.
dmg: app
	@set -eu; stage=$$(mktemp -d /tmp/pubcraft-dmg-XXXXXX); trap 'rm -rf "$$stage"' EXIT; \
	cp -R $(APP) "$$stage/"; ln -s /Applications "$$stage/Applications"; \
	cp README.md README.it.md "$$stage/"; \
	hdiutil create -volname PubCraft -srcfolder "$$stage" -ov -format UDZO $(DMG)

pkg-macos: dmg
	@set -eu; stage=$$(mktemp -d /tmp/pubcraft-mac-XXXXXX); trap 'rm -rf "$$stage"' EXIT; \
	mkdir "$$stage/portable" "$$stage/pkg"; \
	cp $(BIN) $(GUI) $(HTML2PDF) README.md README.it.md LICENSE VERSION "$$stage/portable/"; \
	cp -R fonts third-party-licenses "$$stage/portable/"; cp src/install.sh "$$stage/portable/"; \
	find "$$stage/portable" -type f -exec chmod 644 {} +; \
	chmod 755 "$$stage/portable/pubcraft" "$$stage/portable/pubcraftgui" "$$stage/portable/pubcraft-html2pdf" "$$stage/portable/install.sh"; \
	tar --uid 0 --gid 0 --uname root --gname wheel -czf build/PubCraft-$(VERSION)-macos.tar.gz -C "$$stage/portable" .; \
	cp -R $(APP) "$$stage/pkg/"; \
	pkgbuild --root "$$stage/pkg" --identifier com.pubcraft.app --version $(PKG_VERSION) --install-location /Applications build/PubCraft-$(VERSION).pkg
	python3 tests/check_packages.py build/PubCraft-$(VERSION)-macos.tar.gz
	hdiutil verify $(DMG)

check: $(BIN) $(GUI)
	bash tests/test_cli.sh $(BIN)
	go -C src/pubcraftgui test -race -count=1 -v .

check-browser: all
	npm --prefix tests test

verify-packages:
	python3 tests/check_packages.py

linux: linux-amd64 linux-arm64
linux-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go -C src/pubcraftgui build -trimpath -o ../../build/pubcraftgui-linux-amd64 .
linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go -C src/pubcraftgui build -trimpath -o ../../build/pubcraftgui-linux-arm64 .

# Native Linux/Windows packaging is also automated in release-build.yml.
# Linux requires dpkg-deb and checksum-verified APPIMAGETOOL/APPIMAGE_RUNTIME.
pkg-linux: linux-amd64
	@test -f build/pubcraft-linux-amd64 && ! test $(SRC) -nt build/pubcraft-linux-amd64 || { echo 'Rebuild the native Linux C binary first.'; exit 1; }
	python3 scripts/package_release.py linux
appimage: pkg-linux

pkg-windows:
	@test -f build/pubcraft-windows-amd64.exe && ! test $(SRC) -nt build/pubcraft-windows-amd64.exe || { echo 'Rebuild the native Windows C binary first.'; exit 1; }
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go -C src/pubcraftgui build -trimpath -ldflags '-H=windowsgui' -o ../../build/pubcraftgui-windows-amd64.exe .
	python3 scripts/package_release.py windows

dist: pkg-macos pkg-linux pkg-windows
clean:
	rm -rf build

.PHONY: all install uninstall app dmg pkg-macos check check-browser verify-packages linux linux-amd64 linux-arm64 pkg-linux appimage pkg-windows dist clean
