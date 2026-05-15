// Ad-hoc sign the macOS .app so Apple Silicon kernel will load it.
// Without this, arm64 builds either crash on launch or report "已损坏".
// We still don't have an Apple Developer ID — Gatekeeper will still warn
// on first launch, but at least the binary becomes runnable.
const { execSync } = require('child_process');
const path = require('path');

exports.default = async function (context) {
  if (context.electronPlatformName !== 'darwin') return;
  const appName = context.packager.appInfo.productFilename;
  const appPath = path.join(context.appOutDir, `${appName}.app`);
  console.log(`[afterPack] ad-hoc signing ${appPath}`);
  execSync(`codesign --force --deep --sign - "${appPath}"`, { stdio: 'inherit' });
  // Verify the signature took
  execSync(`codesign --verify --deep --strict --verbose=2 "${appPath}"`, { stdio: 'inherit' });
};
