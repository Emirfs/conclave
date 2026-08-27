Unicode true

####
## Conclave installer.
##
## Derived from the Wails NSIS template. Two things differ from the default,
## and both matter:
##
##  1. Conclave is two binaries, not one. The desktop window is a client; the
##     daemon owns the state. An installer that shipped only the window would
##     install an app that cannot do anything.
##  2. install.ps1 travels with the build. The in-app "Güncelle" button runs
##     the copy sitting next to the application, so a machine installed from
##     this .exe updates itself the same way one installed from the one-line
##     command does.
##
## Regenerate wails_tools.nsh (not this file) with:
##   wails build --target windows/amd64 --nsis
####

# The window's binary is conclave-desktop.exe, but the product is Conclave;
# without this the installed folder and the uninstall entry would both carry
# the internal name.
!define INFO_PRODUCTNAME "Conclave"
!define PRODUCT_EXECUTABLE "conclave-desktop.exe"

# A per-user install: Conclave keeps its state under the user's profile and
# runs provider CLIs as that user, so nothing here needs administrator rights.
!define REQUEST_EXECUTION_LEVEL "user"

!include "wails_tools.nsh"

# The version information for this two must consist of 4 parts
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

# Enable HiDPI support. https://nsis.sourceforge.io/Reference/ManifestDPIAware
ManifestDPIAware true

!include "MUI.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_FINISHPAGE_NOAUTOCLOSE # Wait on the INSTFILES page so the user can take a look into the details of the installation steps
!define MUI_ABORTWARNING # This will warn the user if they exit from the installer.

# Offer to open the app at the end, rather than making the user hunt for the
# shortcut that was just created.
!define MUI_FINISHPAGE_RUN "$INSTDIR\${PRODUCT_EXECUTABLE}"
!define MUI_FINISHPAGE_RUN_TEXT "Conclave'i ac"

!insertmacro MUI_PAGE_WELCOME # Welcome to the installer page.
!insertmacro MUI_PAGE_DIRECTORY # In which folder install page.
!insertmacro MUI_PAGE_INSTFILES # Installing page.
!insertmacro MUI_PAGE_FINISH # Finished installation page.

!insertmacro MUI_UNPAGE_INSTFILES # Uinstalling page

!insertmacro MUI_LANGUAGE "Turkish"
!insertmacro MUI_LANGUAGE "English"

Name "${INFO_PRODUCTNAME}"
# Named for the platform rather than the Wails project, so the release asset
# and the file the user downloads carry the same name.
OutFile "..\..\bin\conclave-windows-${ARCH}-setup.exe"
!ifdef WAILS_INSTALL_SCOPE
  !if "${WAILS_INSTALL_SCOPE}" == "user"
    InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
  !else
    InstallDir "$PROGRAMFILES64\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
  !endif
!else
  InstallDir "$PROGRAMFILES64\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
!endif # Default installing folder ($PROGRAMFILES is Program Files folder).
ShowInstDetails show # This will always show the installation details.

Function .onInit
   !insertmacro wails.checkArchitecture
FunctionEnd

Section
    !insertmacro wails.setShellContext

    !insertmacro wails.webview2runtime

    SetOutPath $INSTDIR

    !insertmacro wails.files

    # The daemon and the scripts the shortcuts and the update button call.
    File "..\..\bin\conclave.exe"
    File "..\..\..\..\..\install.ps1"
    File "..\..\..\..\..\scripts\conclave-durdur.cmd"

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    # Closing the window leaves the daemon running on purpose, so "close
    # everything" needs a control of its own.
    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME} - Kapat.lnk" "$INSTDIR\conclave-durdur.cmd" "" "$INSTDIR\${PRODUCT_EXECUTABLE}" 0 SW_SHOWMINIMIZED

    !insertmacro wails.associateFiles
    !insertmacro wails.associateCustomProtocols

    !insertmacro wails.writeUninstaller
SectionEnd

Section "uninstall"
    !insertmacro wails.setShellContext

    # A running daemon would hold its own binary open and survive the uninstall.
    nsExec::Exec 'taskkill /F /IM conclave-desktop.exe'
    nsExec::Exec 'taskkill /F /IM conclave.exe'

    RMDir /r "$AppData\${PRODUCT_EXECUTABLE}" # Remove the WebView2 DataPath

    RMDir /r $INSTDIR

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME} - Kapat.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

    !insertmacro wails.unassociateFiles
    !insertmacro wails.unassociateCustomProtocols

    !insertmacro wails.deleteUninstaller
SectionEnd
