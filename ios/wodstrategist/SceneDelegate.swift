import UIKit
import React
internal import ExpoModulesCore

class SceneDelegate: UIResponder, UIWindowSceneDelegate {
  var window: UIWindow?

  func scene(
    _ scene: UIScene,
    willConnectTo session: UISceneSession,
    options connectionOptions: UIScene.ConnectionOptions
  ) {
    guard let windowScene = scene as? UIWindowScene else { return }

    let window = UIWindow(windowScene: windowScene)
    self.window = window

    guard let appDelegate = UIApplication.shared.delegate as? AppDelegate,
          let factory = appDelegate.reactNativeFactory else {
      return
    }
    appDelegate.window = window

    factory.startReactNative(
      withModuleName: "main",
      in: window,
      launchOptions: appDelegate.initialLaunchOptions
    )
    window.makeKeyAndVisible()

    // Process initial deep link URLs
    for urlContext in connectionOptions.urlContexts {
      var options: [UIApplication.OpenURLOptionsKey: Any] = [:]
      if let annotation = urlContext.options.annotation {
        options[.annotation] = annotation
      }
      if let sourceApp = urlContext.options.sourceApplication {
        options[.sourceApplication] = sourceApp
      }
      options[.openInPlace] = urlContext.options.openInPlace
      _ = appDelegate.application(UIApplication.shared, open: urlContext.url, options: options)
    }

    // Process initial user activities (Universal Links)
    for userActivity in connectionOptions.userActivities {
      _ = appDelegate.application(
        UIApplication.shared,
        continue: userActivity,
        restorationHandler: { _ in }
      )
    }
  }

  func scene(_ scene: UIScene, openURLContexts URLContexts: Set<UIOpenURLContext>) {
    guard let appDelegate = UIApplication.shared.delegate as? AppDelegate else { return }
    for urlContext in URLContexts {
      var options: [UIApplication.OpenURLOptionsKey: Any] = [:]
      if let annotation = urlContext.options.annotation {
        options[.annotation] = annotation
      }
      if let sourceApp = urlContext.options.sourceApplication {
        options[.sourceApplication] = sourceApp
      }
      options[.openInPlace] = urlContext.options.openInPlace
      _ = appDelegate.application(UIApplication.shared, open: urlContext.url, options: options)
    }
  }

  func scene(_ scene: UIScene, continue userActivity: NSUserActivity) {
    guard let appDelegate = UIApplication.shared.delegate as? AppDelegate else { return }
    _ = appDelegate.application(
      UIApplication.shared,
      continue: userActivity,
      restorationHandler: { _ in }
    )
  }

  func sceneDidBecomeActive(_ scene: UIScene) {
    ExpoAppDelegateSubscriberManager.applicationDidBecomeActive(UIApplication.shared)
  }

  func sceneWillResignActive(_ scene: UIScene) {
    ExpoAppDelegateSubscriberManager.applicationWillResignActive(UIApplication.shared)
  }

  func sceneDidEnterBackground(_ scene: UIScene) {
    ExpoAppDelegateSubscriberManager.applicationDidEnterBackground(UIApplication.shared)
  }

  func sceneWillEnterForeground(_ scene: UIScene) {
    ExpoAppDelegateSubscriberManager.applicationWillEnterForeground(UIApplication.shared)
  }
}

