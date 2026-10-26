VERSION=$(shell tr -d '\r\n' < VERSION)
PKG_VERSION=1.0.0.2
GOFLAGS=-trimpath
export COPYFILE_DISABLE=1
CC=clang
CFLAGS=-std=c11 -Wall -Wextra -O2
PREFIX=/usr/local
BIN=build/pubcraft
SRC=src/pubcraft.c
GUI=build/pubcraftgui
GUISRC=$(wildcard src/pubcraftgui/*.go) src/pubcraftgui/go.mod src/pubcraftgui/go.sum
GUIWEB=$(shell find src/pubcraftgui/web -type f)
FONTFILES=$(shell find fonts -type f)
THIRDPARTY=$(shell find third-party-licenses -type f)
HTML2PDF=build/pubcraft-html2pdf
HTML2PDFSRC=src/pubcraft-html2pdf.m
APP=build/PubCraft.app
DMG=build/PubCraft-$(VERSION).dmg

all: $(BIN) $(GUI) $(HTML2PDF)

$(BIN): $(SRC)
	@mkdir -p build
	$(CC) $(CFLAGS) -o $(BIN) $(SRC)

# Generatore nativo PDF per macOS basato su WebKit (zero dipendenze esterne)
$(HTML2PDF): $(HTML2PDFSRC)
	@mkdir -p build
	@if [ "$$(uname -s)" = "Darwin" ]; then \
		clang -O2 -fmodules -framework Cocoa -framework WebKit -o $(HTML2PDF) $(HTML2PDFSRC) 2>/dev/null || true; \
	fi

# GUI web locale (binario unico con interfaccia incorporata)
$(GUI): $(GUISRC) $(GUIWEB)
	@mkdir -p build
	go -C src/pubcraftgui build $(GOFLAGS) -o ../../build/pubcraftgui .

# installazione nel sistema (binary + man page)
install: $(BIN) $(GUI)
	install -d $(DESTDIR)$(PREFIX)/bin $(DESTDIR)$(PREFIX)/share/man/man1
	install -m 0755 $(BIN) $(DESTDIR)$(PREFIX)/bin/pubcraft
	install -m 0755 $(GUI) $(DESTDIR)$(PREFIX)/bin/pubcraftgui
	install -m 0755 src/pubcraft-mcp $(DESTDIR)$(PREFIX)/bin/pubcraft-mcp
	install -d $(DESTDIR)$(PREFIX)/share/pubcraft/fonts
	cp -R fonts/. $(DESTDIR)$(PREFIX)/share/pubcraft/fonts/
	install -d $(DESTDIR)$(PREFIX)/share/doc/pubcraft
	cp -R third-party-licenses $(DESTDIR)$(PREFIX)/share/doc/pubcraft/
	@if [ -f $(HTML2PDF) ]; then install -m 0755 $(HTML2PDF) $(DESTDIR)$(PREFIX)/bin/pubcraft-html2pdf; fi
	@echo "installato: $(PREFIX)/bin/pubcraft + $(PREFIX)/bin/pubcraftgui + $(PREFIX)/bin/pubcraft-mcp"
	install -m 0644 man/pubcraft.1 $(DESTDIR)$(PREFIX)/share/man/man1/pubcraft.1
	@echo "installato manuale: $(PREFIX)/share/man/man1/pubcraft.1"

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/pubcraft $(DESTDIR)$(PREFIX)/bin/pubcraftgui $(DESTDIR)$(PREFIX)/bin/pubcraft-mcp $(DESTDIR)$(PREFIX)/bin/pubcraft-html2pdf $(DESTDIR)$(PREFIX)/share/man/man1/pubcraft.1

# app bundle macOS autonoma (include sia pubcraft che pubcraftgui)
app: $(BIN) $(GUI) $(HTML2PDF) $(FONTFILES) $(THIRDPARTY)
	rm -rf $(APP)
	osacompile -o $(APP) src/PubCraft.applescript
	cp $(BIN) $(APP)/Contents/MacOS/
	cp $(GUI) $(APP)/Contents/MacOS/
	@if [ -f $(HTML2PDF) ]; then cp $(HTML2PDF) $(APP)/Contents/MacOS/; fi
	cp -R fonts $(APP)/Contents/Resources/fonts
	cp -R third-party-licenses $(APP)/Contents/Resources/
	cp src/AppIcon.icns $(APP)/Contents/Resources/droplet.icns
	cp src/AppIcon.icns $(APP)/Contents/Resources/applet.icns 2>/dev/null || true
	/usr/libexec/PlistBuddy -c "Add :CFBundleShortVersionString string $(VERSION)" $(APP)/Contents/Info.plist
	/usr/libexec/PlistBuddy -c "Add :CFBundleVersion string $(PKG_VERSION)" $(APP)/Contents/Info.plist
	codesign --force --sign - $(APP)/Contents/MacOS/pubcraft
	codesign --force --sign - $(APP)/Contents/MacOS/pubcraftgui
	@if [ -f $(APP)/Contents/MacOS/pubcraft-html2pdf ]; then codesign --force --sign - $(APP)/Contents/MacOS/pubcraft-html2pdf; fi
	codesign --force --sign - $(APP)
	codesign --verify --deep --strict $(APP)
	@echo "app pronta (firma ad-hoc, non notarizzata): $(APP)"

# immagine disco con app + scorciatoia Applicazioni + istruzioni
dmg: app
	rm -f $(DMG)
	STAGE=$$(mktemp -d /tmp/pubcraft-dmg-XXXXXX); \
	cp -R $(APP) "$$STAGE/"; \
	ln -s /Applications "$$STAGE/Applicazioni"; \
	cp README.md "$$STAGE/Leggimi.txt"; \
	hdiutil create -volname "PubCraft" -srcfolder "$$STAGE" -ov -format UDZO /tmp/pubcraft-tmp.dmg; \
	rm -rf "$$STAGE"; \
	mv /tmp/pubcraft-tmp.dmg $(DMG)
	@echo "dmg pronto: $(DMG)"

check: $(BIN) $(GUI)
	@echo "===> Esecuzione suite di test CLI (tests/test_cli.sh)..."
	@chmod +x tests/test_cli.sh
	@./tests/test_cli.sh $(BIN)
	@echo "===> Esecuzione test unitari Go GUI (go test con race detector)..."
	@go -C src/pubcraftgui test -race -v .
	@echo "===> Tutti i test sono passati con successo!"

check-browser: all
	node tests/test_preview_geometry.cjs && \
	NODE_PATH=tests/node_modules node tests/test_audit4_fixes.cjs && \
	NODE_PATH=tests/node_modules node tests/test_languages_preview.cjs && \
	NODE_PATH=tests/node_modules node tests/test_transitions_and_export.cjs && \
	NODE_PATH=tests/node_modules node tests/test_project_safety.cjs && \
	NODE_PATH=tests/node_modules node tests/test_i18n_and_preview.cjs && \
	NODE_PATH=tests/node_modules node tests/test_publication_preview.cjs && \
	NODE_PATH=tests/node_modules node tests/test_editor_toolbar.cjs && \
	NODE_PATH=tests/node_modules node tests/test_welcome_and_recents.cjs && \
	NODE_PATH=tests/node_modules node tests/test_notebook_and_title.cjs && \
	NODE_PATH=tests/node_modules node tests/test_focus_mode.cjs && \
	NODE_PATH=tests/node_modules node tests/test_dark_mode.cjs && \
	NODE_PATH=tests/node_modules node tests/test_topbar_responsiveness.cjs && \
	NODE_PATH=tests/node_modules node tests/test_line_breaks.cjs && \
	NODE_PATH=tests/node_modules node tests/test_residual_selection.cjs

verify-packages:
	python3 tests/check_packages.py

clean:
	rm -rf build

# ---- build per Linux (server / NAS / VPS) --------------------------------
linux: linux-amd64 linux-arm64

linux-amd64: $(GUISRC) $(GUIWEB)
	cd src/pubcraftgui && GOOS=linux GOARCH=amd64 go build -trimpath \
		-o ../../build/pubcraftgui-linux-amd64 .
	@echo "build/pubcraftgui-linux-amd64"

linux-arm64: $(GUISRC) $(GUIWEB)
	cd src/pubcraftgui && GOOS=linux GOARCH=arm64 go build -trimpath \
		-o ../../build/pubcraftgui-linux-arm64 .
	@echo "build/pubcraftgui-linux-arm64"

# ---- pacchetti di distribuzione (macOS, Linux, Windows) --------------------
dist: pkg-macos pkg-linux pkg-windows

pkg-macos: dmg
	@mkdir -p /tmp/pubcraft-mac-stage
	@cp $(BIN) /tmp/pubcraft-mac-stage/
	@cp $(GUI) /tmp/pubcraft-mac-stage/
	@if [ -f $(HTML2PDF) ]; then cp $(HTML2PDF) /tmp/pubcraft-mac-stage/; fi
	@cp README.md LICENSE /tmp/pubcraft-mac-stage/
	@cp -R third-party-licenses /tmp/pubcraft-mac-stage/
	@cp -R fonts /tmp/pubcraft-mac-stage/
	@cp src/install.sh /tmp/pubcraft-mac-stage/install.sh
	@chmod +x /tmp/pubcraft-mac-stage/install.sh
	@tar -czf $(CURDIR)/build/PubCraft-$(VERSION)-macos.tar.gz -C /tmp/pubcraft-mac-stage .
	@rm -rf /tmp/pubcraft-mac-stage
	@mkdir -p /tmp/pubcraft-pkg-root && cp -R $(APP) /tmp/pubcraft-pkg-root/ && pkgbuild --root /tmp/pubcraft-pkg-root --identifier com.pubcraft.app --version $(PKG_VERSION) --install-location /Applications $(CURDIR)/build/PubCraft-$(VERSION).pkg >/dev/null 2>&1 && rm -rf /tmp/pubcraft-pkg-root
	@echo "Pacchetto macOS pronto: build/PubCraft-$(VERSION).dmg, build/PubCraft-$(VERSION).pkg e build/PubCraft-$(VERSION)-macos.tar.gz"

pkg-linux: linux-amd64 appimage
	@if [ ! -f build/pubcraft-linux-amd64 ] || [ $(SRC) -nt build/pubcraft-linux-amd64 ]; then \
		echo "ERRORE: ricompilare build/pubcraft-linux-amd64 dai sorgenti attuali."; \
		exit 1; \
	fi
	@mkdir -p /tmp/pubcraft-linux-stage
	@cp build/pubcraft-linux-amd64 /tmp/pubcraft-linux-stage/pubcraft
	@cp build/pubcraftgui-linux-amd64 /tmp/pubcraft-linux-stage/pubcraftgui
	@cp README.md LICENSE /tmp/pubcraft-linux-stage/
	@cp -R third-party-licenses /tmp/pubcraft-linux-stage/
	@cp -R fonts /tmp/pubcraft-linux-stage/
	@if [ -f src/pubcraft.png ]; then cp src/pubcraft.png /tmp/pubcraft-linux-stage/; fi
	@if [ -f src/pubcraft.desktop ]; then cp src/pubcraft.desktop /tmp/pubcraft-linux-stage/; fi
	@cp src/install.sh /tmp/pubcraft-linux-stage/install.sh
	@chmod +x /tmp/pubcraft-linux-stage/install.sh
	@tar -czf $(CURDIR)/build/PubCraft-$(VERSION)-linux-x64.tar.gz -C /tmp/pubcraft-linux-stage .
	@rm -rf /tmp/pubcraft-linux-stage
	@if which docker >/dev/null 2>&1; then \
		docker run --rm -v "$(CURDIR)":/work -w /work debian:bookworm-slim sh -c '\
			set -e; \
			PKG="/tmp/pkg_deb"; rm -rf "$$PKG"; \
			mkdir -p "$$PKG/DEBIAN" "$$PKG/usr/bin" "$$PKG/usr/share/applications" "$$PKG/usr/share/icons/hicolor/256x256/apps" "$$PKG/usr/share/doc/pubcraft"; \
			printf "Package: pubcraft\nVersion: $(VERSION)\nSection: text\nPriority: optional\nArchitecture: amd64\nMaintainer: PubCraft Contributors <info@pubcraft.org>\nDepends: zip, unzip, chromium | chromium-browser | google-chrome-stable\nDescription: Markdown Book Formatter & Publisher (Amazon KDP & EPUB)\n" > "$$PKG/DEBIAN/control"; \
			cp build/pubcraft-linux-amd64 "$$PKG/usr/bin/pubcraft"; \
			cp build/pubcraftgui-linux-amd64 "$$PKG/usr/bin/pubcraftgui"; \
			mkdir -p "$$PKG/usr/share/pubcraft/fonts"; cp -R fonts/. "$$PKG/usr/share/pubcraft/fonts/"; \
			chmod 755 "$$PKG/usr/bin/pubcraft" "$$PKG/usr/bin/pubcraftgui"; \
			cp src/pubcraft.desktop "$$PKG/usr/share/applications/pubcraft.desktop"; \
			if [ -f src/pubcraft.png ]; then cp src/pubcraft.png "$$PKG/usr/share/icons/hicolor/256x256/apps/pubcraft.png"; fi; \
			cp README.md "$$PKG/usr/share/doc/pubcraft/"; \
			cp LICENSE "$$PKG/usr/share/doc/pubcraft/copyright"; \
			cp -R third-party-licenses "$$PKG/usr/share/doc/pubcraft/"; \
			dpkg-deb --build "$$PKG" build/PubCraft-$(VERSION)-linux-amd64.deb >/dev/null 2>&1; \
			rm -rf "$$PKG"; \
		'; \
	else \
		echo "Docker richiesto per generare il DEB." >&2; exit 1; \
	fi
	@echo "Pacchetti Linux pronti in build/:"
	@echo "  - build/PubCraft-$(VERSION)-x86_64.AppImage (Auto-eseguibile Linux x86_64)"
	@echo "  - build/PubCraft-$(VERSION)-linux-amd64.deb   (Debian, Ubuntu, Mint)"
	@echo "  - build/PubCraft-$(VERSION)-linux-x64.tar.gz  (Arch, Fedora, manuale)"

appimage: linux-amd64
	@if which docker >/dev/null 2>&1; then \
		docker run --rm -v "$(CURDIR)":/work -w /work debian:bookworm-slim sh -c '\
			set -e; \
			export DEBIAN_FRONTEND=noninteractive; \
			apt-get update -qq && apt-get install -y -qq --no-install-recommends curl ca-certificates file zsync gcc musl-tools >/dev/null; \
			if [ ! -f /work/build/pubcraft-linux-amd64 ] || [ /work/src/pubcraft.c -nt /work/build/pubcraft-linux-amd64 ]; then \
				musl-gcc -O3 -Wall -static /work/src/pubcraft.c -o /work/build/pubcraft-linux-amd64; \
			fi; \
			mkdir -p /tmp/tool && cd /tmp/tool; \
			curl -sSL https://github.com/AppImage/AppImageKit/releases/download/continuous/appimagetool-x86_64.AppImage -o appimagetool; \
			chmod +x appimagetool; \
			./appimagetool --appimage-extract >/dev/null; \
			APPDIR="/tmp/PubCraft.AppDir"; rm -rf "$$APPDIR"; \
			mkdir -p "$$APPDIR/usr/bin" "$$APPDIR/usr/share/applications" "$$APPDIR/usr/share/icons/hicolor/256x256/apps"; \
			cp /work/build/pubcraft-linux-amd64 "$$APPDIR/usr/bin/pubcraft"; \
			cp /work/build/pubcraftgui-linux-amd64 "$$APPDIR/usr/bin/pubcraftgui"; \
			mkdir -p "$$APPDIR/usr/share/doc/pubcraft"; cp -R /work/third-party-licenses "$$APPDIR/usr/share/doc/pubcraft/"; \
			mkdir -p "$$APPDIR/usr/share/pubcraft/fonts"; cp -R /work/fonts/. "$$APPDIR/usr/share/pubcraft/fonts/"; \
			chmod 755 "$$APPDIR/usr/bin/pubcraft" "$$APPDIR/usr/bin/pubcraftgui"; \
			cp /work/src/pubcraft.desktop "$$APPDIR/pubcraft.desktop"; \
			cp /work/src/pubcraft.desktop "$$APPDIR/usr/share/applications/pubcraft.desktop"; \
			cp /work/src/pubcraft.png "$$APPDIR/pubcraft.png"; \
			cp /work/src/pubcraft.png "$$APPDIR/usr/share/icons/hicolor/256x256/apps/pubcraft.png"; \
			printf "#!/bin/sh\nset -e\nHERE=\"\$$(dirname \"\$$(readlink -f \"\$${0}\")\")\"\nexport PATH=\"\$${HERE}/usr/bin:\$${PATH}\"\nexec \"\$${HERE}/usr/bin/pubcraftgui\" \"\$$@\"\n" > "$$APPDIR/AppRun"; \
			chmod 755 "$$APPDIR/AppRun"; \
			rm -f /work/build/PubCraft-$(VERSION)-x86_64.AppImage; \
			ARCH=x86_64 /tmp/tool/squashfs-root/AppRun "$$APPDIR" /tmp/PubCraft-$(VERSION)-x86_64.AppImage >/dev/null 2>&1; \
			mv -f /tmp/PubCraft-$(VERSION)-x86_64.AppImage /work/build/PubCraft-$(VERSION)-x86_64.AppImage; \
			chmod +x /work/build/PubCraft-$(VERSION)-x86_64.AppImage; \
			rm -rf /tmp/tool "$$APPDIR"; \
		' || exit $$?; \
		echo "AppImage Linux pronto: build/PubCraft-$(VERSION)-x86_64.AppImage"; \
	else \
		echo "Docker non disponibile per generare l'AppImage." >&2; exit 1; \
	fi

pkg-windows:
	@if [ ! -f build/pubcraft-windows-amd64.exe ] || [ $(SRC) -nt build/pubcraft-windows-amd64.exe ]; then \
		echo "ERRORE: ricompilare build/pubcraft-windows-amd64.exe dai sorgenti attuali."; \
		echo "       Serve per il Setup NSIS: cross-compila il C (mingw-w64 o zig cc)"; \
		echo "       oppure copialo in build/ prima di 'make pkg-windows'."; \
		exit 1; \
	fi
	@cd src/pubcraftgui && GOOS=windows GOARCH=amd64 go build -trimpath \
		-ldflags "-H=windowsgui" -o ../../build/pubcraftgui-windows-amd64.exe .
	@mkdir -p /tmp/pubcraft-win-stage
	@if [ -f build/pubcraft-windows-amd64.exe ]; then cp build/pubcraft-windows-amd64.exe /tmp/pubcraft-win-stage/pubcraft.exe; fi
	@cp build/pubcraftgui-windows-amd64.exe /tmp/pubcraft-win-stage/pubcraftgui.exe
	@cp -R fonts /tmp/pubcraft-win-stage/
	@if [ -f src/app.ico ]; then cp src/app.ico /tmp/pubcraft-win-stage/; fi
	@printf '@echo off\r\nstart "" "%%~dp0pubcraftgui.exe"\r\n' > /tmp/pubcraft-win-stage/Avvia-PubCraft.bat
	@cp README.md /tmp/pubcraft-win-stage/README.txt
	@cp LICENSE /tmp/pubcraft-win-stage/LICENSE.txt
	@cp -R third-party-licenses /tmp/pubcraft-win-stage/
	@(cd /tmp/pubcraft-win-stage && zip -qr $(CURDIR)/build/PubCraft-$(VERSION)-windows-x64.zip .)
	@rm -rf /tmp/pubcraft-win-stage
	@if which docker >/dev/null 2>&1; then \
		docker run --rm -v "$(CURDIR)":/work -w /work/src debian:bookworm-slim sh -c "apt-get update -qq && apt-get install -y -qq nsis >/dev/null && makensis installer.nsi >/dev/null 2>&1"; \
	else \
		echo "Docker/NSIS richiesto per generare il Setup Windows." >&2; exit 1; \
	fi
	@echo "Pacchetto Windows pronto: build/PubCraft-Setup-$(VERSION).exe e build/PubCraft-$(VERSION)-windows-x64.zip"

.PHONY: all install uninstall app dmg check check-browser verify-packages clean linux linux-amd64 linux-arm64 dist pkg-macos pkg-linux pkg-windows appimage
