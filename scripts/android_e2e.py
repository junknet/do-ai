import os
import time
import subprocess
import sys

APK_PATH = "app/android-rn/android/app/build/outputs/apk/debug/app-debug.apk"
PACKAGE_NAME = "com.anonymous.androidrn"
MAIN_ACTIVITY = ".MainActivity"

def run_cmd(cmd):
    print(f"Running: {cmd}")
    result = subprocess.run(cmd, shell=True, capture_output=True, text=True)
    if result.returncode != 0:
        print(f"Error: {result.stderr}")
    return result.stdout.strip()

def main():
    # 1. Check Device
    devices = run_cmd("adb devices")
    if "device" not in devices.split("\n")[1]:
        print("No device connected.")
        sys.exit(1)
    
    print("Device connected.")

    # 2. Install APK
    print("Installing APK...")
    run_cmd(f"adb install -r {APK_PATH}")

    # 3. Launch App
    print("Launching App...")
    run_cmd(f"adb shell am start -n {PACKAGE_NAME}/{MAIN_ACTIVITY}")
    
    # Wait for app to load (Expo/React Native takes a moment)
    print("Waiting for app load...")
    time.sleep(8) 

    # 4. Screenshot Home
    print("Taking Home Screenshot...")
    run_cmd("adb shell screencap -p /sdcard/home.png")
    run_cmd("adb pull /sdcard/home.png reports/android_e2e_home.png")

    # 5. Interact: Tap first session card
    # Approx coords based on 1080x2340: 
    # Header area is top. Session list is below meta row.
    # Let's tap x=200, y=600.
    print("Tapping Session Card...")
    run_cmd("adb shell input tap 200 600")
    
    time.sleep(3)

    # 6. Screenshot Terminal (Initial)
    print("Taking Terminal Screenshot...")
    run_cmd("adb shell screencap -p /sdcard/terminal.png")
    run_cmd("adb pull /sdcard/terminal.png reports/android_e2e_terminal.png")

    # 7. Interact: Type command
    print("Typing command 'ls'...")
    # Click input box at bottom? It might be auto-focused or accessible.
    # We can try just sending text.
    run_cmd("adb shell input text 'ls'")
    time.sleep(1)
    # Enter key (KEYCODE_ENTER = 66)
    run_cmd("adb shell input keyevent 66")
    
    time.sleep(3)

    # 8. Screenshot Terminal (Result)
    print("Taking Terminal Result Screenshot...")
    run_cmd("adb shell screencap -p /sdcard/terminal_result.png")
    run_cmd("adb pull /sdcard/terminal_result.png reports/android_e2e_terminal_result.png")

    print("Done. Check reports/ folder.")

if __name__ == "__main__":
    main()
