!macro NSIS_HOOK_PREUNINSTALL
  ${If} $DeleteAppDataCheckboxState = 1
  ${AndIf} $UpdateMode <> 1
    IfFileExists "$LOCALAPPDATA\${BUNDLEID}\wsl\installation.json" 0 rex_wsl_cleanup_done
    MessageBox MB_YESNO|MB_DEFBUTTON2 "Also remove Rex's Linux command environment and package caches? Other WSL distributions are kept. Choosing No preserves Rex's local app data." IDYES rex_remove_wsl IDNO rex_keep_data
    rex_remove_wsl:
      !insertmacro CheckIfAppIsRunning "${MAINBINARYNAME}.exe" "${PRODUCTNAME}"
      nsExec::ExecToStack /TIMEOUT=120000 '"$INSTDIR\rex-host.exe" --remove-sandbox "$LOCALAPPDATA\${BUNDLEID}"'
      Pop $0
      Pop $1
      ${If} $0 != 0
        StrCpy $DeleteAppDataCheckboxState 0
        MessageBox MB_OK "Rex could not safely remove its command environment. Local app data has been preserved."
      ${EndIf}
      Goto rex_wsl_cleanup_done
    rex_keep_data:
      StrCpy $DeleteAppDataCheckboxState 0
    rex_wsl_cleanup_done:
  ${EndIf}
!macroend
