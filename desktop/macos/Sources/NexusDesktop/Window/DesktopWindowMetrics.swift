import CoreGraphics

enum DesktopWindowMetrics {
  static let dragRegionHeight: CGFloat = 34
  static let trafficLightReservedWidth: CGFloat = 84

  // MARK: - 动态窗口尺寸常量

  /// 设计基准宽度
  static let designWidth: CGFloat = 1280
  /// 设计基准高度
  static let designHeight: CGFloat = 820
  /// 绝对下限宽度（保证 UI 控件不溢出）
  static let absoluteFloorWidth: CGFloat = 960
  /// 绝对下限高度
  static let absoluteFloorHeight: CGFloat = 560

  /// 初始窗口宽度占工作区宽度比例
  private static let workAreaScale: CGFloat = 0.90
  /// 窗口高度占用工作区高度上限比例
  private static let workAreaHeightCap: CGFloat = 0.88

  /// 最小尺寸相对于初始宽度的比例 1120/1280
  private static let minSizeScale: CGFloat = 1120 / 1280

  struct WindowSizePreset {
    let width: CGFloat
    let height: CGFloat
    let minWidth: CGFloat
    let minHeight: CGFloat
  }

  /// 根据屏幕可用工作区计算窗口初始大小与最小尺寸
  static func preset(for workArea: CGSize) -> WindowSizePreset {
    let designRatio = designHeight / designWidth
    let minRatio: CGFloat = 640 / 1120

    // 1) 初始宽度：工作区宽度 * 比例，落在绝对下限与设计基准之间
    var width: CGFloat = workArea.width * workAreaScale
    width = min(max(width, absoluteFloorWidth), designWidth)

    // 2) 初始高度按比例计算
    var height: CGFloat = width * designRatio

    // 3) 高度溢出时按高度反推
    let maxHeight = workArea.height * workAreaHeightCap
    if height > maxHeight {
      height = maxHeight
      width = height / designRatio
    }

    // 4) 应用绝对下限
    width = max(width, absoluteFloorWidth)
    height = max(height, absoluteFloorHeight)

    // 5) 最小尺寸：基于实际窗口大小缩放，但不低于绝对下限的缩放值
    let minWidth = max(width * minSizeScale, absoluteFloorWidth * minSizeScale)
    let minHeight = max(minWidth * minRatio, absoluteFloorHeight * minSizeScale * minRatio)

    return WindowSizePreset(
      width: width.rounded(.towardZero),
      height: height.rounded(.towardZero),
      minWidth: minWidth.rounded(.towardZero),
      minHeight: minHeight.rounded(.towardZero)
    )
  }

  /// 根据可用工作区计算 WKWebView 内容缩放因子
  static func contentScale(for workArea: CGSize) -> CGFloat {
    let preset = self.preset(for: workArea)
    let scale = min(preset.width / designWidth, preset.height / designHeight)
    return min(max(scale, 0.25), 1.0)
  }
}
