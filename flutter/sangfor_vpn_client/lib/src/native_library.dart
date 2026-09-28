import 'dart:ffi';
import 'dart:io';

/// Opens the zju-connect native library.
///
/// The library file name differs per platform. On iOS the Go code is compiled
/// as a static archive and linked into the app, so the symbols live in the
/// process image rather than in a separate dynamic library.
DynamicLibrary openZjuConnectLibrary() {
  if (Platform.isIOS) {
    // Symbols are linked into the executable (c-archive), so look them up in
    // the running process.
    return DynamicLibrary.process();
  }
  if (Platform.isMacOS) {
    // A macOS Flutter app bundles the dylib in Frameworks/; a pure Dart test
    // can still dlopen it by name if it sits next to the executable.
    if (Platform.environment.containsKey('ZJU_CONNECT_LIBRARY')) {
      return DynamicLibrary.open(Platform.environment['ZJU_CONNECT_LIBRARY']!);
    }
    return DynamicLibrary.open('libzju_connect.dylib');
  }
  if (Platform.isWindows) {
    return DynamicLibrary.open('zju_connect.dll');
  }
  // Linux and Android.
  return DynamicLibrary.open('libzju_connect.so');
}
