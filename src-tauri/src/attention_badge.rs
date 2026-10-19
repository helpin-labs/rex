use tauri::image::Image;

pub fn image(count: u32) -> Option<Image<'static>> {
    if count == 0 { return None; }
    let label = if count > 99 { "99+".to_string() } else { count.to_string() };
    let glyphs = [
        0b111_101_101_101_111, 0b010_110_010_010_111, 0b111_001_111_100_111,
        0b111_001_111_001_111, 0b101_101_111_001_001, 0b111_100_111_001_111,
        0b111_100_111_101_111, 0b111_001_001_001_001, 0b111_101_111_101_111,
        0b111_101_111_001_111, 0b000_010_111_010_000,
    ];
    let mut rgba = vec![0u8; 32 * 32 * 4];
    for y in 0..32i32 { for x in 0..32i32 {
        if (x - 16).pow(2) + (y - 16).pow(2) < 256 {
            let offset = (y as usize * 32 + x as usize) * 4;
            rgba[offset..offset + 4].copy_from_slice(&[220, 38, 38, 255]);
        }
    }}
    let scale = if label.len() > 2 { 2 } else { 3 };
    let left = (32 - (3 * scale + 2) * label.len() + 2) / 2;
    let top = (32 - 5 * scale) / 2;
    for (index, digit) in label.bytes().enumerate() {
        let glyph = glyphs[if digit == b'+' { 10 } else { (digit - b'0') as usize }];
        for y in 0..5 { for x in 0..3 {
            if glyph & (1 << (14 - y * 3 - x)) == 0 { continue; }
            for dy in 0..scale { for dx in 0..scale {
                let px = left + index * (3 * scale + 2) + x * scale + dx;
                let py = top + y * scale + dy;
                let offset = (py * 32 + px) * 4;
                rgba[offset..offset + 4].copy_from_slice(&[255, 255, 255, 255]);
            }}
        }}
    }
    Some(Image::new_owned(rgba, 32, 32))
}

#[cfg(test)]
mod tests {
    use super::image;
    #[test]
    fn clears_zero_and_caps_large_counts() {
        assert!(image(0).is_none());
        assert_ne!(image(1).unwrap().rgba(), image(2).unwrap().rgba());
        assert_eq!(image(100).unwrap().rgba(), image(u32::MAX).unwrap().rgba());
        assert!(image(99).unwrap().rgba().chunks(4).any(|pixel| pixel == [255, 255, 255, 255]));
    }
}
