.vu
.global add
add:
  --enter
  --endenter
  lq.xyzw vf02, 0(vi00)
  lq.xyzw vf03, 1(vi00)
  add.xyzw vf01, vf02, vf03
  sq.xyzw vf01, 2(vi00)
  --exit
  --endexit
END
