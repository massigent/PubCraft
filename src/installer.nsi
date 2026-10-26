; PubCraft Windows Installer Script (NSIS)
!include "MUI2.nsh"
!define PUBCRAFT_VERSION "1.0.0-alpha.2"

Name "PubCraft"
OutFile "../build/PubCraft-Setup-${PUBCRAFT_VERSION}.exe"
InstallDir "$PROGRAMFILES64\PubCraft"
InstallDirRegKey HKLM "Software\PubCraft" "Install_Dir"
RequestExecutionLevel admin

; UI Settings
!define MUI_ABORTWARNING
!define MUI_ICON "app.ico"
!define MUI_UNICON "app.ico"
!define MUI_WELCOMEPAGE_TITLE "Benvenuto nell'installazione di PubCraft"
!define MUI_WELCOMEPAGE_TEXT "PubCraft permette di scrivere libri in Markdown ed esportare EPUB e impaginati di stampa. Verifica sempre i file con il servizio di pubblicazione scelto.$\r$\n$\r$\nFai clic su Avanti per continuare."

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "../LICENSE"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES

!define MUI_FINISHPAGE_RUN "$INSTDIR\pubcraftgui.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Avvia PubCraft subito"
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "Italian"

Section "PubCraft Core" SecCore
  SetOutPath "$INSTDIR"

  File "../build/pubcraft-windows-amd64.exe"
  Rename "$INSTDIR\pubcraft-windows-amd64.exe" "$INSTDIR\pubcraft.exe"

  File "../build/pubcraftgui-windows-amd64.exe"
  Rename "$INSTDIR\pubcraftgui-windows-amd64.exe" "$INSTDIR\pubcraftgui.exe"

  File "app.ico"
  File /oname=README.txt "../README.md"
  File /oname=LICENSE.txt "../LICENSE"
  SetOutPath "$INSTDIR\third-party-licenses"
  File /r "../third-party-licenses/*"
  SetOutPath "$INSTDIR\fonts"
  File /r "../fonts/*"
  SetOutPath "$INSTDIR"

  ; Registro di sistema
  WriteRegStr HKLM "Software\PubCraft" "Install_Dir" "$INSTDIR"

  ; Disinstallazione in Installazione Applicazioni di Windows
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\PubCraft" "DisplayName" "PubCraft"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\PubCraft" "DisplayVersion" "${PUBCRAFT_VERSION}"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\PubCraft" "Publisher" "PubCraft Contributors"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\PubCraft" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\PubCraft" "DisplayIcon" '"$INSTDIR\app.ico"'
  WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\PubCraft" "NoModify" 1
  WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\PubCraft" "NoRepair" 1
  WriteUninstaller "$INSTDIR\uninstall.exe"

  ; Collegamenti Menu Start
  CreateDirectory "$SMPROGRAMS\PubCraft"
  CreateShortcut "$SMPROGRAMS\PubCraft\PubCraft.lnk" "$INSTDIR\pubcraftgui.exe" "" "$INSTDIR\app.ico" 0
  CreateShortcut "$SMPROGRAMS\PubCraft\Disinstalla PubCraft.lnk" "$INSTDIR\uninstall.exe" "" "$INSTDIR\app.ico" 0

  ; Collegamento sul Desktop
  CreateShortcut "$DESKTOP\PubCraft.lnk" "$INSTDIR\pubcraftgui.exe" "" "$INSTDIR\app.ico" 0
SectionEnd

Section "Uninstall"
  Delete "$DESKTOP\PubCraft.lnk"
  Delete "$SMPROGRAMS\PubCraft\PubCraft.lnk"
  Delete "$SMPROGRAMS\PubCraft\Disinstalla PubCraft.lnk"
  RMDir "$SMPROGRAMS\PubCraft"

  Delete "$INSTDIR\pubcraft.exe"
  Delete "$INSTDIR\pubcraftgui.exe"
  Delete "$INSTDIR\app.ico"
  Delete "$INSTDIR\README.txt"
  Delete "$INSTDIR\LICENSE.txt"
  Delete "$INSTDIR\uninstall.exe"
  RMDir /r "$INSTDIR\fonts"
  RMDir /r "$INSTDIR\third-party-licenses"
  RMDir "$INSTDIR"

  DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\PubCraft"
  DeleteRegKey HKLM "Software\PubCraft"
SectionEnd
