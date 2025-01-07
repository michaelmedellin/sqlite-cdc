set terminal svg background rgb "#FFFFFF"
set title "Wide Tables With With Triggers"
set border lc rgb 'black'
set key outside right vertical tc rgb 'black'
set linetype 1 lc rgb 'black'
set linetype 2 lc rgb 'blue'
set linetype 3 lc rgb 'dark-grey'

set style fill solid
set boxwidth .5


set xlabel 'Columns' tc rgb 'black'
set style data histograms

set ylabel 'Percent Worse (%)' tc rgb 'black'
set yrange [0.0:50000.0]
set logscale y 2

$data << EOF
64 119 225 251
128 195 335 556
256 412 696 1434
512 1011 1558 4948
1000 3263 3872 33814
EOF

plot \
    "$data" using 2:xtic(1) title "INSERT", \
    "" using 3 title "UPDATE", \
    "" using 4 title "DELETE"
